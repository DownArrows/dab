package main

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/russross/blackfriday/v2"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	markdownExtensions = blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis
	markdownOptions    = blackfriday.WithExtensions(blackfriday.Extensions(markdownExtensions))
)

var matchTags = regexp.MustCompile("<([^>]*)>")

// HTTPCacheMaxAge is the maxmimum cache age one can set in response to an HTTP request.
const HTTPCacheMaxAge = 31536000
const (
	// WebServerListenFD is the file descriptor number onto which the web server will serve its content
	// if activation basde on file descriptor is enabled.
	WebServerListenFD = 3
	// WebServerListenFDRedirector is the file descriptor number onto which the HTTPS redirector will run
	// if activation basde on file descriptor is enabled.
	WebServerListenFDRedirector = 4
)

// URLFromHostPort returns an URL from a listen specification compatible with the standard library,
// adding the "listen on all" host if the host is empty.
func URLFromHostPort(hostport string) (*url.URL, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, err
	}
	if host == "" {
		host = "0.0.0.0"
	}
	return &url.URL{Host: net.JoinHostPort(host, port)}, nil
}

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
	actual   *http.ServeMux
	logger   LevelLogger
	IPHeader string
}

// NewServeMux returns a ServeMux wrapping an http.NewServeMux.
func NewServeMux(logger LevelLogger, ipHeader string) *ServeMux {
	return &ServeMux{
		actual:   http.NewServeMux(),
		logger:   logger,
		IPHeader: ipHeader,
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
			mux.logger.Errord(func() error {
				return fmt.Errorf("error in response to %s %s for %s: %v",
					r.Method, r.URL, getIP(r, mux.IPHeader), err)
			})
		}
	}()
	mux.logger.Infod(func() interface{} {
		return fmt.Sprintf("serve %s %s for %s with user agent %q",
			r.Method, r.URL, getIP(r, mux.IPHeader), r.Header.Get("User-Agent"))
	})
	w := NewResponseWriter(baseWriter, r)
	mux.actual.ServeHTTP(w, r)
	if err := w.Close(); err != nil {
		panic(err)
	}
}

func getIP(r *http.Request, header string) string {
	if header == "" {
		return r.RemoteAddr
	}
	return r.Header.Get(header)
}

// Redirector is a simple HTTP server that redirects to another URL,
// used with WebServer for forcing HTTPS.
type Redirector struct {
	RedirectorConf
	logger   LevelLogger
	SelfLink string
	server   *http.Server
}

// NewRedirector creates a new redirection server.
func NewRedirector(logger LevelLogger, conf RedirectorConf) (*Redirector, error) {
	rdr := &Redirector{
		RedirectorConf: conf,
		logger:         logger,
		server:         &http.Server{Addr: conf.Listen},
	}
	if self_link, err := rdr.getSelfLink(); err != nil {
		return nil, err
	} else {
		rdr.SelfLink = self_link
	}
	rdr.server.Handler = rdr
	return rdr, nil
}

func (rdr *Redirector) getSelfLink() (string, error) {
	if rdr.ListenFDs > 0 {
		return fmt.Sprintf("file descriptor %d", WebServerListenFDRedirector), nil
	}
	self_link, err := URLFromHostPort(rdr.Listen)
	if err != nil {
		return "", err
	}
	self_link.Scheme = "http"
	return self_link.String(), nil
}

// Run is a Task that blocks until the context is cancelled, thereby shutting down the redirection server.
func (rdr *Redirector) Run(ctx Ctx) error {
	tasks := NewTaskGroup(ctx)
	tasks.SpawnCtx(func(_ Ctx) error { return rdr.listen() })
	tasks.SpawnCtx(webServerShutdown(rdr.server))
	rdr.logger.Infof("redirector listening on %s", rdr.SelfLink)
	return tasks.Wait().ToError()
}

func (rdr *Redirector) listen() error {
	var err error
	var listener net.Listener

	if rdr.ListenFDs > 0 {
		fd := os.NewFile(WebServerListenFDRedirector, "redirector")
		defer fd.Close()
		listener, err = net.FileListener(fd)
		if err != nil {
			msg := "error with the redirector's file descriptor %d when trying to create a listener on it: %v"
			return fmt.Errorf(msg, WebServerListenFDRedirector, err)
		}
	} else {
		listener, err = net.Listen("tcp", rdr.Listen)
	}
	defer listener.Close()

	return ignoreWebServerCloseErr(rdr.server.Serve(listener))
}

func (rdr *Redirector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rdr.logger.Infod(func() interface{} {
		return fmt.Sprintf("ignoring %s %s from %s with user agent %q and force redirect to https",
			r.Method, r.URL, getIP(r, rdr.IPHeader), r.Header.Get("User-Agent"))
	})
	http.Redirect(w, r, rdr.Target, http.StatusSeeOther)
}

// WebServer serves the stored data as HTML pages and a backup of the database.
type WebServer struct {
	WebConf
	cert struct {
		sync.Mutex
		value *tls.Certificate
	}
	compendium CompendiumFactory
	conns      StorageConnPool
	logger     LevelLogger
	redirector *Redirector
	reports    ReportFactory
	SelfLink   string
	server     *http.Server
	storage    *Storage
}

// NewWebServer creates a new WebServer.
func NewWebServer(
	logger LevelLogger,
	storage *Storage,
	reports ReportFactory,
	compendium CompendiumFactory,
	conf WebConf,
) (*WebServer, error) {

	var err error

	wsrv := &WebServer{
		WebConf:    conf,
		compendium: compendium,
		logger:     logger,
		reports:    reports,
		storage:    storage,
	}

	if err := wsrv.SetCertificate(conf); err != nil {
		return nil, err
	}

	if wsrv.TLS.Enabled() && wsrv.TLS.Redirector.Enabled() {
		wsrv.redirector, err = NewRedirector(wsrv.logger, wsrv.TLS.Redirector)
		if err != nil {
			return nil, err
		}
	}

	if link, err := wsrv.getSelfLink(); err != nil {
		return nil, err
	} else {
		wsrv.SelfLink = link
	}

	mux := NewServeMux(wsrv.logger, wsrv.IPHeader)
	mux.HandleFunc("/css/", wsrv.immutableCache(wsrv.CSS))
	mux.HandleFunc("/reports", wsrv.ReportIndex)
	mux.HandleFunc("/reports/", wsrv.Report)
	mux.HandleFunc("/reports/current", wsrv.ReportCurrent)
	mux.HandleFunc("/reports/lastweek", wsrv.ReportLatest)
	mux.HandleFunc("/reports/source/", wsrv.ReportSource)
	mux.HandleFunc("/reports/stats/", wsrv.ReportStats)
	mux.HandleFunc("/compendium", wsrv.CompendiumIndex)
	mux.HandleFunc("/compendium/user/", wsrv.CompendiumUser)
	mux.HandleFunc("/compendium/comments", wsrv.CompendiumComments)
	mux.HandleFunc("/compendium/comments/user/", wsrv.CompendiumUserComments)
	mux.HandleFunc("/backup", wsrv.Backup)
	if conf.RootDir != "" {
		wsrv.logger.Infof("serving directory %q", wsrv.RootDir)
		mux.Handle("/", http.FileServer(http.Dir(wsrv.RootDir)))
	}

	wsrv.server = &http.Server{Addr: conf.Listen, Handler: mux}

	return wsrv, nil
}

// SetCertificate sets and can hot-swap the TLS certificate.
func (wsrv *WebServer) SetCertificate(conf WebConf) error {
	if conf.TLS.Enabled() {
		wsrv.cert.Lock()
		defer wsrv.cert.Unlock()
		if cert, err := tls.LoadX509KeyPair(conf.TLS.Cert, conf.TLS.Key); err != nil {
			return err
		} else {
			wsrv.cert.value = &cert
		}
	}
	return nil
}

// Run runs the web server and blocks until it is cancelled or returns an error.
func (wsrv *WebServer) Run(ctx Ctx) error {
	pool, err := wsrv.initDBPool(ctx)
	if err != nil {
		return err
	}
	wsrv.conns = pool
	defer wsrv.conns.Close()

	tasks := NewTaskGroup(ctx)

	tasks.SpawnCtx(func(_ Ctx) error { return wsrv.listen() })
	tasks.SpawnCtx(webServerShutdown(wsrv.server))

	if wsrv.TLS.Enabled() && wsrv.TLS.Redirector.Enabled() {
		tasks.SpawnCtx(wsrv.redirector.Run)
	}

	if interval := wsrv.DBOptimize.Value; interval != 0 {
		tasks.SpawnCtx(func(ctx Ctx) error { return wsrv.conns.Analyze(ctx, interval) })
	}

	return tasks.Wait().ToError()
}

func (wsrv *WebServer) initDBPool(ctx Ctx) (StorageConnPool, error) {
	pool, err := NewStorageConnPool(ctx, wsrv.NbDBConn, wsrv.getConn)
	if wsrv.DirtyReads && err == nil {
		wsrv.logger.Info("dirty reads of the database enabled")
	}
	return pool, nil
}

func (wsrv *WebServer) getConn(ctx Ctx) (StorageConn, error) {
	conn, err := wsrv.storage.GetConn(ctx)
	if err != nil {
		return conn, err
	}
	if wsrv.DirtyReads {
		return conn, conn.ReadUncommitted(true)
	}
	return conn, nil
}

func (wsrv *WebServer) listen() error {
	var err error
	var listener net.Listener

	if wsrv.ListenFDs > 0 {
		fd := os.NewFile(WebServerListenFD, "web server")
		defer fd.Close()
		if listener, err = net.FileListener(fd); err != nil {
			msg := "error with the file descriptor %d when trying to create a listener on it: %v"
			return fmt.Errorf(msg, WebServerListenFD, err)
		}
	} else {
		if listener, err = net.Listen("tcp", wsrv.Listen); err != nil {
			return err
		}
	}
	defer listener.Close()

	if wsrv.TLS.Enabled() {
		tls_conf := &tls.Config{
			GetCertificate: wsrv.getCertificate,
			MinVersion:     tls.VersionTLS12,

			PreferServerCipherSuites: false,
		}
		listener = tls.NewListener(listener, tls_conf)
		wsrv.logger.Infof("TLS enabled on %s", wsrv.Listen)
		defer listener.Close()
	}

	wsrv.logger.Infof("listening on %s", wsrv.SelfLink)

	return ignoreWebServerCloseErr(wsrv.server.Serve(listener))
}

func (wsrv *WebServer) getCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	wsrv.cert.Lock()
	defer wsrv.cert.Unlock()
	return wsrv.cert.value, nil
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
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// ReportSource serves the reports in markdown format according to the year and week in the URL.
func (wsrv *WebServer) ReportSource(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/source/", r)))
	if err != nil {
		wsrv.err(w, r, err, http.StatusBadRequest)
		return
	}

	var report Report
	err = wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		var err error
		report, err = wsrv.reports.ReportWeek(conn, week, year)
		return err
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusInternalServerError)
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

// ReportStats serves an HTML document of the statistics for the year and week in the URL.
func (wsrv *WebServer) ReportStats(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/stats/", r)))
	if err != nil {
		wsrv.err(w, r, err, http.StatusBadRequest)
		return
	}

	var data ReportHeader
	err = wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		var err error
		data, err = wsrv.reports.StatsWeek(conn, week, year)
		return err
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusInternalServerError)
		return
	} else if data.Len == 0 {
		wsrv.errMsg(w, r, fmt.Sprintf("No statistics for %d/%d.", year, week), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "ReportStats", data); err != nil {
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

	var report Report
	err = wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		var err error
		report, err = wsrv.reports.ReportWeek(conn, week, year)
		return err
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusInternalServerError)
		return
	} else if report.Len() == 0 {
		wsrv.errMsg(w, r, fmt.Sprintf("Empty report for %d/%d.", year, week), http.StatusNotFound)
		return
	}

	report.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "Report", report); err != nil {
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
	var compendium Compendium
	err := wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		var err error
		compendium, err = wsrv.compendium.Index(conn)
		return err
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusInternalServerError)
		return
	}

	compendium.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "Compendium", compendium); err != nil {
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
	var stats CompendiumUser

	err := wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		var err error
		stats, err = wsrv.compendium.User(conn, username)
		if err != nil {
			wsrv.err(w, r, err, http.StatusInternalServerError)
			return ErrSentinel
		} else if !stats.Exists() {
			wsrv.errMsg(w, r, fmt.Sprintf("User %q doesn't exist.", username), http.StatusNotFound)
			return ErrSentinel
		}
		return nil
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}

	stats.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "CompendiumUser", stats); err != nil {
		panic(err)
	}
}

// CompendiumUserComments serves the comments of a user.
func (wsrv *WebServer) CompendiumUserComments(w http.ResponseWriter, r *http.Request) {
	args := ignoreTrailing(subPath("/compendium/comments/user/", r))
	if len(args) != 1 {
		msg := "invalid URL, use \"/compendium/comments/user/username\" to view the comments of \"username\""
		wsrv.errMsg(w, r, msg, http.StatusBadRequest)
		return
	}
	username := args[0]

	page, err := wsrv.pagination(r.URL.Query())
	if err != nil {
		wsrv.err(w, r, err, http.StatusBadRequest)
		return
	}

	var comments CompendiumUser

	err = wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		var err error
		comments, err = wsrv.compendium.UserComments(conn, username, page)
		if err != nil {
			wsrv.err(w, r, err, http.StatusInternalServerError)
			return ErrSentinel
		} else if !comments.Exists() {
			wsrv.errMsg(w, r, fmt.Sprintf("User %q doesn't exist.", username), http.StatusNotFound)
			return ErrSentinel
		}
		return nil
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}

	comments.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "CompendiumUserComments", comments); err != nil {
		panic(err)
	}
}

// CompendiumComments serves the paginated HTML document of all known comments from non-hidden users.
func (wsrv *WebServer) CompendiumComments(w http.ResponseWriter, r *http.Request) {
	page, err := wsrv.pagination(r.URL.Query())
	if err != nil {
		wsrv.err(w, r, err, http.StatusBadRequest)
		return
	}

	var comments Compendium
	err = wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		var err error
		comments, err = wsrv.compendium.Comments(conn, page)
		if err != nil {
			wsrv.err(w, r, err, http.StatusInternalServerError)
			return ErrSentinel
		}
		return nil
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusServiceUnavailable)
		return
	}

	comments.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "CompendiumComments", comments); err != nil {
		panic(err)
	}
}

// Backup triggers a backup if needed, and serves it.
func (wsrv *WebServer) Backup(w http.ResponseWriter, r *http.Request) {
	err := wsrv.conns.WithConn(r.Context(), func(conn StorageConn) error {
		return wsrv.storage.Backup(r.Context(), conn)
	})
	if err != nil {
		wsrv.err(w, r, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-sqlite3")
	http.ServeFile(w, r, wsrv.storage.BackupPath())
}

func (wsrv *WebServer) err(w http.ResponseWriter, r *http.Request, err error, code int) {
	var msg string
	if err == ErrSentinel {
		return
	} else if IsCancellation(err) {
		msg = "Server shutting down."
		code = http.StatusServiceUnavailable
	} else {
		str := fmt.Sprint(err)
		msg = fmt.Sprintf("%s%s.", strings.ToUpper(str[0:1]), str[1:])
	}
	wsrv.errMsg(w, r, msg, code)
}

func (wsrv *WebServer) errMsg(w http.ResponseWriter, r *http.Request, msg string, code int) {
	wsrv.logger.Errord(func() error {
		return fmt.Errorf("error %d %q in response to %s %s for %s with user agent %q",
			code, msg, r.Method, r.URL, getIP(r, wsrv.IPHeader), r.Header.Get("User-Agent"))
	})
	http.Error(w, msg, code)
}

func (wsrv *WebServer) getSelfLink() (string, error) {
	if wsrv.ListenFDs > 0 {
		return fmt.Sprintf("file descriptor %d", WebServerListenFD), nil
	}
	address, err := URLFromHostPort(wsrv.Listen)
	if err != nil {
		return "", err
	}
	if wsrv.TLS.Enabled() {
		address.Scheme = "https"
	} else {
		address.Scheme = "http"
	}
	return address.String(), nil
}

func (wsrv *WebServer) commentBodyConverter(src CommentView) (interface{}, error) {
	// We replace < and > with look-alikes because blackfriday's HTML renderer is poorly configurable,
	// and writing a replacement would be a timesink considering the original isn't very straightforward.
	body := matchTags.ReplaceAllString(src.Body, "\u2329$1\u232a")
	html := blackfriday.Run([]byte(body), markdownOptions)
	return template.HTML(html), nil
}

func redirectToReport(week uint8, year int, w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, fmt.Sprintf("/reports/%d/%d", year, week), http.StatusTemporaryRedirect)
}

func (wsrv *WebServer) immutableCache(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
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

func (wsrv *WebServer) pagination(urlQuery url.Values) (Pagination, error) {
	var page Pagination

	limit, err := urlQueryIntParameter(urlQuery, "limit")
	if err != nil {
		return page, err
	} else if limit < 0 {
		return page, errors.New("negative limits are not allowed")
	}

	page.Limit = uint(limit)
	if page.Limit > wsrv.MaxLimit {
		return page, fmt.Errorf("maximum number of items per page is %d", wsrv.MaxLimit)
	} else if limit == 0 {
		page.Limit = wsrv.DefaultLimit
	}

	offset, err := urlQueryIntParameter(urlQuery, "offset")
	if offset < 0 {
		return page, errors.New("negative offsets are not allowed")
	}
	page.Offset = uint(offset)

	return page, err
}

func ignoreWebServerCloseErr(err error) error {
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func webServerShutdown(srv *http.Server) Task {
	return func(ctx Ctx) error {
		<-ctx.Done()
		return srv.Shutdown(context.Background())
	}
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

func urlQueryIntParameter(query url.Values, name string) (int, error) {
	if raw, ok := query[name]; ok {
		if len(raw) > 1 {
			return 0, fmt.Errorf("only one %q parameter is accepted", name)
		}
		return strconv.Atoi(raw[0])
	}
	return 0, nil
}
