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
}

// NewServeMux returns a ServeMux wrapping an http.NewServeMux.
func NewServeMux() *ServeMux {
	return &ServeMux{actual: http.NewServeMux()}
}

// HandleFunc wrapper.
func (mux *ServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.actual.HandleFunc(pattern, handler)
}

// ServeHTTP wrapper.
func (mux *ServeMux) ServeHTTP(baseW http.ResponseWriter, r *http.Request) {
	w := NewResponseWriter(baseW, r)
	mux.actual.ServeHTTP(w, r)
	if err := w.Close(); err != nil {
		panic(err)
	}
}

func immutableCache(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != Version.String() {
			http.NotFound(w, r)
			return
		}

		if r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", HTTPCacheMaxAge))

		handler(w, r)
	}
}

// WebServer serves the stored data as HTML pages and a backup of the database.
type WebServer struct {
	compendium      CompendiumFactory
	done            chan error
	markdownOptions blackfriday.Option
	reports         ReportFactory
	server          *http.Server
	storage         WebServerStorage
}

// NewWebServer creates a new WebServer.
func NewWebServer(conf WebConf, storage WebServerStorage, reports ReportFactory, compendium CompendiumFactory) *WebServer {
	mdExts := blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis

	wsrv := &WebServer{
		compendium:      compendium,
		done:            make(chan error),
		markdownOptions: blackfriday.WithExtensions(blackfriday.Extensions(mdExts)),
		reports:         reports,
		storage:         storage,
	}

	mux := NewServeMux()
	mux.HandleFunc("/css/", immutableCache(wsrv.CSS))
	mux.HandleFunc("/reports", wsrv.ReportIndex)
	mux.HandleFunc("/reports/", wsrv.Report)
	mux.HandleFunc("/reports/current", wsrv.ReportCurrent)
	mux.HandleFunc("/reports/lastweek", wsrv.ReportLatest)
	mux.HandleFunc("/reports/source/", wsrv.ReportSource)
	mux.HandleFunc("/compendium", wsrv.CompendiumIndex)
	mux.HandleFunc("/compendium/user/", wsrv.CompendiumUser)
	mux.HandleFunc("/backup", wsrv.Backup)

	wsrv.server = &http.Server{Addr: conf.Listen, Handler: mux}

	return wsrv
}

func (wsrv *WebServer) fatal(err error) {
	wsrv.done <- err
}

// Run runs the web server and blocks until it is cancelled or returns an error.
func (wsrv *WebServer) Run(ctx context.Context) error {
	go func() {
		wsrv.done <- wsrv.server.ListenAndServe()
	}()

	var err error
	select {
	case <-ctx.Done():
		wsrv.server.Close()
	case err = <-wsrv.done:
		break
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
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
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(css))
}

// ReportIndex serves the reports' index (unimplemented).
func (wsrv *WebServer) ReportIndex(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// ReportSource serves the reports in markdown format according to the year and week in the URL.
func (wsrv *WebServer) ReportSource(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/source/", r)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	report, err := wsrv.reports.ReportWeek(r.Context(), week, year)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	} else if report.Len() == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := MarkdownReport.Execute(w, report); err != nil {
		wsrv.fatal(err)
	}
}

// Report serves the HTML reports according to the year and week in the URL.
func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/", r)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report, err := wsrv.reports.ReportWeek(r.Context(), week, year)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	} else if report.Len() == 0 {
		http.NotFound(w, r)
		return
	}

	report.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLReportPage.Execute(w, report); err != nil {
		wsrv.fatal(err)
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
	stats, err := wsrv.compendium.Compendium(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "invalid URL, use \"/compendium/user/username\" to view the page about \"username\"", http.StatusBadRequest)
		return
	}
	username := args[0]

	query := wsrv.storage.GetUser(r.Context(), username)
	if !query.Exists {
		http.Error(w, fmt.Sprintf("user %q doesn't exist", username), http.StatusNotFound)
		return
	} else if query.Error != nil {
		http.Error(w, query.Error.Error(), http.StatusInternalServerError)
		return
	}

	stats, err := wsrv.compendium.User(r.Context(), query.User)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	if err := wsrv.storage.Backup(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/x-sqlite3")
	http.ServeFile(w, r, wsrv.storage.BackupPath())
}

func (wsrv *WebServer) commentBodyConverter(src CommentView) (interface{}, error) {
	html := blackfriday.Run([]byte(src.Body), wsrv.markdownOptions)
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
