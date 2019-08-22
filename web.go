package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"github.com/russross/blackfriday"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

var (
	markdownExtensions = blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis
	markdownOptions    = blackfriday.WithExtensions(blackfriday.Extensions(markdownExtensions))
)

// HTTPCacheMaxAge is the maxmimum cache age one can set in response to an HTTP request.
const HTTPCacheMaxAge = 31536000

// ResponseWriter is a wrapper for http.ResponseWriter with basic GZip compression support.
type ResponseWriter struct {
	actual http.ResponseWriter
	gzip   *gzip.Writer
}

// NewResponseWriter wraps standard ResponseWriter, and uses an *http.Request to test for GZip support.
func NewResponseWriter(w http.ResponseWriter, r *http.Request) *ResponseWriter {
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			panic(err)
		}
		return &ResponseWriter{actual: w, gzip: gw}
	}
	return &ResponseWriter{actual: w}
}

// Close closes the writer, which is required by the underlying GZip writer.
func (w *ResponseWriter) Close() error {
	if w.gzip == nil {
		return nil
	}
	return w.gzip.Close()
}

// Header wrapper.
func (w *ResponseWriter) Header() http.Header {
	return w.actual.Header()
}

// Write wrapper.
func (w *ResponseWriter) Write(data []byte) (int, error) {
	if w.gzip == nil {
		return w.actual.Write(data)
	}
	return w.gzip.Write(data)
}

// WriteHeader wrapper.
func (w *ResponseWriter) WriteHeader(statusCode int) {
	w.actual.WriteHeader(statusCode)
}

// ServeMux is a minimal wrapper for http.ServeMux uses our ResponseWriter
type ServeMux struct {
	actual *http.ServeMux
	logger LevelLogger
}

// NewServeMux returns a ServeMux wrapping an http.NewServeMux.
func NewServeMux(logger LevelLogger) *ServeMux {
	return &ServeMux{
		actual: http.NewServeMux(),
		logger: logger,
	}
}

// HandleFunc wrapper.
func (mux *ServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.actual.HandleFunc(pattern, handler)
}

// Handle wrapper.
func (mux *ServeMux) Handle(pattern string, handler http.Handler) {
	mux.actual.Handle(pattern, handler)
}

// ServeHTTP wrapper.
func (mux *ServeMux) ServeHTTP(baseWriter http.ResponseWriter, r *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			mux.logger.Errorf("error in response to %s %s for %s: %v", r.Method, r.URL, r.RemoteAddr, err)
		}
	}()
	mux.logger.Infof("serve %s %s for %s with user agent %q", r.Method, r.URL, r.RemoteAddr, r.Header.Get("User-Agent"))
	w := NewResponseWriter(baseWriter, r)
	mux.actual.ServeHTTP(w, r)
	if err := w.Close(); err != nil {
		panic(err)
	}
}

func immutableCache(wsrv *WebServer, handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		requestedVersion := r.URL.Query().Get("version")
		// leave the empty version as a special case for easy linking from a custom HTML file without the need to constantly update it
		if requestedVersion != "" {
			if requestedVersion != Version.String() {
				msg := fmt.Sprintf("Current version is %q, file for version %q is unavailable.", Version, requestedVersion)
				wsrv.errMsg(w, r, msg, http.StatusNotFound)
				return
			}

			if r.Header.Get("If-Modified-Since") != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", HTTPCacheMaxAge))
		}

		handler(w, r)
	}
}

// WebServer serves the stored data as HTML pages and a backup of the database.
type WebServer struct {
	sync.Mutex

	// configuration
	dirtyReads bool
	nbDBConn   uint

	// dependencies
	compendium CompendiumFactory
	conns      *SQLiteConnPool
	logger     LevelLogger
	reports    ReportFactory
	server     *http.Server
	storage    WebServerStorage

	// other
	done chan error
}

// NewWebServer creates a new WebServer.
func NewWebServer(logger LevelLogger, storage WebServerStorage, reports ReportFactory, compendium CompendiumFactory, conf WebConf) *WebServer {
	wsrv := &WebServer{
		// configuration
		dirtyReads: conf.DirtyReads,
		nbDBConn:   conf.NbDBConn,
		// dependencies
		compendium: compendium,
		logger:     logger,
		reports:    reports,
		storage:    storage,
		// other
		done: make(chan error),
	}

	mux := NewServeMux(wsrv.logger)
	mux.HandleFunc("/css/", immutableCache(wsrv, wsrv.CSS))
	mux.HandleFunc("/reports", wsrv.ReportIndex)
	mux.HandleFunc("/reports/", wsrv.Report)
	mux.HandleFunc("/reports/current", wsrv.ReportCurrent)
	mux.HandleFunc("/reports/lastweek", wsrv.ReportLatest)
	mux.HandleFunc("/reports/source/", wsrv.ReportSource)
	mux.HandleFunc("/compendium", wsrv.CompendiumIndex)
	mux.HandleFunc("/compendium/user/", wsrv.CompendiumUser)
	mux.HandleFunc("/backup", wsrv.Backup)
	if conf.RootDir != "" {
		wsrv.logger.Infof("web server serving the directory %q", conf.RootDir)
		mux.Handle("/", http.FileServer(http.Dir(conf.RootDir)))
	}

	wsrv.server = &http.Server{Addr: conf.Listen, Handler: mux}

	return wsrv
}

// Run runs the web server and blocks until it is cancelled or returns an error.
func (wsrv *WebServer) Run(ctx context.Context) error {
	wsrv.conns = NewSQLiteConnPool(ctx, wsrv.nbDBConn)
	defer wsrv.conns.Close()

	if wsrv.dirtyReads {
		wsrv.logger.Info("web server's dirty reads of the database enabled")
	}

	for i := uint(0); i < wsrv.nbDBConn; i++ {
		conn, err := wsrv.storage.GetConn(ctx)
		if err != nil {
			return err
		}
		if wsrv.dirtyReads {
			if err := conn.ReadUncommitted(true); err != nil {
				return err
			}
		}
		wsrv.conns.Release(conn)
	}

	go func() { wsrv.done <- wsrv.server.ListenAndServe() }()

	wsrv.logger.Infof("web server listening on %s", wsrv.server.Addr)

	select {
	case <-ctx.Done():
		return wsrv.server.Shutdown(context.Background())
	case err := <-wsrv.done:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// CSS serves the style sheets.
func (wsrv *WebServer) CSS(w http.ResponseWriter, r *http.Request) {
	var css string
	switch r.URL.Path {
	case "/css/main":
		css = CSSMain
	case "/css/reports":
		css = CSSReports
	case "/css/compendium":
		css = CSSCompendium
	default:
		wsrv.errMsg(w, r, fmt.Sprintf("Stylesheet %q doesn't exist.", r.URL.Path), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(css))
}

// ReportIndex serves the reports' index (unimplemented).
func (wsrv *WebServer) ReportIndex(w http.ResponseWriter, r *http.Request) {
	wsrv.errMsg(w, r, "Index of reports unimplemented.", http.StatusNotFound)
}

// ReportSource serves the reports in markdown format according to the year and week in the URL.
func (wsrv *WebServer) ReportSource(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/source/", r)))
	if err != nil {
		wsrv.err(w, r, err, http.StatusBadRequest)
		return
	}

	conn, err := wsrv.conns.Acquire(r.Context())
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}

	report, err := wsrv.reports.ReportWeek(conn, week, year)
	wsrv.conns.Release(conn)
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	} else if report.Len() == 0 {
		wsrv.errMsg(w, r, fmt.Sprintf("Empty report for %d/%d.", year, week), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := MarkdownReport.Execute(w, report); err != nil {
		panic(err)
	}
}

// Report serves the HTML reports according to the year and week in the URL.
func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/", r)))
	if err != nil {
		wsrv.err(w, r, err, http.StatusBadRequest)
		return
	}

	conn, err := wsrv.conns.Acquire(r.Context())
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}

	report, err := wsrv.reports.ReportWeek(conn, week, year)
	wsrv.conns.Release(conn)
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	} else if report.Len() == 0 {
		wsrv.errMsg(w, r, fmt.Sprintf("Empty report for %d/%d.", year, week), http.StatusNotFound)
		return
	}

	report.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLReportPage.Execute(w, report); err != nil {
		panic(err)
	}
}

// ReportCurrent redirects to the report for the current week.
func (wsrv *WebServer) ReportCurrent(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.reports.CurrentWeekCoordinates()
	redirectToReport(week, year, w, r)
}

// ReportLatest redirects to the report for the previous week.
func (wsrv *WebServer) ReportLatest(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.reports.LastWeekCoordinates()
	redirectToReport(week, year, w, r)
}

// CompendiumIndex serves the compendium's index.
func (wsrv *WebServer) CompendiumIndex(w http.ResponseWriter, r *http.Request) {
	conn, err := wsrv.conns.Acquire(r.Context())
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}

	stats, err := wsrv.compendium.Compendium(conn)
	wsrv.conns.Release(conn)
	if err != nil {
		wsrv.err(w, r, err, http.StatusInternalServerError)
		return
	}

	stats.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLCompendium.Execute(w, stats); err != nil {
		panic(err)
	}
}

// CompendiumUser serves the compendium page for a single user, whose name is taken from the URL (case-insensitive).
func (wsrv *WebServer) CompendiumUser(w http.ResponseWriter, r *http.Request) {
	args := ignoreTrailing(subPath("/compendium/user/", r))
	if len(args) != 1 {
		msg := "invalid URL, use \"/compendium/user/username\" to view the page about \"username\""
		wsrv.errMsg(w, r, msg, http.StatusBadRequest)
		return
	}
	username := args[0]

	conn, err := wsrv.conns.Acquire(r.Context())
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}
	defer wsrv.conns.Release(conn)

	query := wsrv.storage.GetUser(conn, username)
	if !query.Exists {
		wsrv.errMsg(w, r, fmt.Sprintf("User %q doesn't exist.", username), http.StatusNotFound)
		return
	} else if query.Error != nil {
		wsrv.err(w, r, query.Error, http.StatusInternalServerError)
		return
	}

	stats, err := wsrv.compendium.User(conn, query.User)
	if err != nil {
		wsrv.err(w, r, err, http.StatusInternalServerError)
		return
	}

	stats.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLCompendiumUserPage.Execute(w, stats); err != nil {
		panic(err)
	}
}

// Backup triggers a backup if needed, and serves it.
func (wsrv *WebServer) Backup(w http.ResponseWriter, r *http.Request) {
	conn, err := wsrv.conns.Acquire(r.Context())
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}
	defer wsrv.conns.Release(conn)

	if err := wsrv.storage.Backup(r.Context(), conn); err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/x-sqlite3")
	http.ServeFile(w, r, wsrv.storage.BackupPath())
}

func (wsrv *WebServer) err(w http.ResponseWriter, r *http.Request, err error, code int) {
	var msg string
	if IsCancellation(err) {
		msg = "server shutting down"
	} else {
		str := fmt.Sprint(err)
		msg = fmt.Sprintf("%s%s.", strings.ToUpper(str[0:1]), str[1:])
	}
	wsrv.errMsg(w, r, msg, code)
}

func (wsrv *WebServer) errMsg(w http.ResponseWriter, r *http.Request, msg string, code int) {
	wsrv.logger.Errorf("error %d %q in response to %s %s for %s with user agent %q",
		code, msg, r.Method, r.URL, r.RemoteAddr, r.Header.Get("User-Agent"))
	http.Error(w, msg, code)
}

func (wsrv *WebServer) commentBodyConverter(src CommentView) (interface{}, error) {
	html := blackfriday.Run([]byte(src.Body), markdownOptions)
	return template.HTML(html), nil
}

func redirectToReport(week uint8, year int, w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, fmt.Sprintf("/reports/%d/%d", year, week), http.StatusTemporaryRedirect)
}

func subPath(prefix string, r *http.Request) []string {
	subURL := r.URL.Path[len(prefix):]
	return strings.Split(subURL, "/")
}

func ignoreTrailing(path []string) []string {
	if len(path) >= 2 && path[len(path)-1] == "" {
		path = path[:len(path)-2]
	}
	return path
}

func weekAndYear(path []string) (uint8, int, error) {
	if len(path) != 2 {
		return 0, 0, errors.New("URL must include '[year]/[week number]'")
	}

	year, err := strconv.Atoi(path[0])
	if err != nil {
		return 0, 0, errors.New("year must be a valid number")
	}

	week, err := strconv.Atoi(path[1])
	if err != nil {
		return 0, 0, errors.New("week must be a valid number")
	}

	if week > 255 || week < 1 {
		return 0, 0, errors.New("week must not be greater than 255 or lower than 1")
	}

	return uint8(week), year, nil
}
