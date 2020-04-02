package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/russross/blackfriday/v2"
	"golang.org/x/oauth2"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
)

// TODO inject authorization on all pages

var (
	markdownExtensions = blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis
	markdownOptions    = blackfriday.WithExtensions(blackfriday.Extensions(markdownExtensions))
)

var matchTags = regexp.MustCompile("<([^>]*)>")

// WebServer serves the stored data as HTML pages and a backup of the database.
type WebServer struct {
	SelfLink string
	WebConf
	acme       *ACMEManager
	compendium CompendiumFactory
	conns      SQLiteConnPool
	helper     *TLSHelper
	logger     LevelLogger
	oAuthConf  *oauth2.Config
	reports    ReportFactory
	server     *http.Server
	sessions   WebSessionFactory
	storage    *Storage
	tls        *tls.Config
}

// NewWebServer creates a new WebServer.
func NewWebServer(
	logger LevelLogger,
	storage *Storage,
	reports ReportFactory,
	compendium CompendiumFactory,
	conf WebConf,
) (*WebServer, error) {

	wsrv := &WebServer{
		WebConf:    conf,
		compendium: compendium,
		sessions:   NewWebSessionFactory("session", conf.Sessions),
		logger:     logger,
		reports:    reports,
		storage:    storage,
		tls: &tls.Config{
			PreferServerCipherSuites: false,
			MinVersion:               tls.VersionTLS12,
		},
		oAuthConf: &oauth2.Config{
			ClientID:     conf.Discord.ClientID,
			ClientSecret: conf.Discord.ClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://discordapp.com/api/oauth2/authorize",
				TokenURL: "https://discordapp.com/api/oauth2/token",
			},
			Scopes: []string{"identify"},
		},
	}

	var err error

	if wsrv.TLS.ACMEEnabled() {
		wsrv.acme, err = NewACMEManager(wsrv.withConn, wsrv.TLS.ACME...)
		if err != nil {
			return nil, err
		}
		wsrv.tls.NextProtos = wsrv.acme.TLSNextProtos()
		wsrv.tls.GetCertificate = wsrv.acme.GetCertificate
	} else if wsrv.TLS.CertsEnabled() {
		cert, err := tls.LoadX509KeyPair(wsrv.TLS.Cert, wsrv.TLS.Key)
		if err != nil {
			return nil, err
		}
		wsrv.tls.Certificates = []tls.Certificate{cert}
	}

	if wsrv.TLS.Helper.Enabled() {
		wsrv.helper, err = NewTLSHelper(wsrv.logger, wsrv.acme, wsrv.TLS.Helper)
		if err != nil {
			return nil, err
		}
	}

	wsrv.SelfLink, err = wsrv.getSelfLink()
	if err != nil {
		return nil, err
	}

	mux := NewServeMux(wsrv.logger, wsrv.IPHeader)
	mux.HandleAuto("/css/", wsrv.immutableCache(wsrv.CSS))
	mux.HandleAuto("/reports", wsrv.ReportIndex)
	mux.HandleAuto("/reports/", wsrv.Report)
	mux.HandleAuto("/reports/current", wsrv.ReportCurrent)
	mux.HandleAuto("/reports/lastweek", wsrv.ReportLatest)
	mux.HandleAuto("/reports/source/", wsrv.ReportSource)
	mux.HandleAuto("/reports/stats/", wsrv.ReportStats)
	mux.HandleAuto("/compendium", wsrv.CompendiumIndex)
	mux.HandleAuto("/compendium/user/", wsrv.CompendiumUser)
	mux.HandleAuto("/compendium/comments", wsrv.CompendiumComments)
	mux.HandleAuto("/compendium/comments/user/", wsrv.CompendiumUserComments)
	mux.HandleAuto("/backup/secrets", wsrv.BackupSecrets)
	mux.HandleAuto("/backup/main", wsrv.BackupMain)
	mux.HandleFunc("/backup", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/backup/main", http.StatusMovedPermanently)
	})
	if conf.RootDir != "" {
		wsrv.logger.Infof("serving directory %q", wsrv.RootDir)
		mux.Handle("/", http.FileServer(http.Dir(wsrv.RootDir)))
	}

	wsrv.server = &http.Server{Addr: conf.Listen, Handler: mux}

	return wsrv, nil
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

	if wsrv.TLS.Enabled() && wsrv.TLS.Helper.Enabled() {
		tasks.SpawnCtx(wsrv.helper.Run)
	}

	tasks.SpawnCtx(func(_ Ctx) error { return wsrv.listen() })
	tasks.SpawnCtx(HTTPServerShutdown(wsrv.server))

	if interval := wsrv.DBOptimize.Value; interval != 0 {

		tasks.SpawnCtx(PeriodicTask(interval/time.Duration(pool.Size()), DefaultPeriodicTaskJitter, func(ctx Ctx) error {
			if err := wsrv.conns.AnalyzeOne(ctx, interval); err != nil {
				wsrv.logger.Errorf("error when analyzing a connection: %v", err)
			}
			return nil
		}))

		tasks.SpawnCtx(PeriodicTask(interval, DefaultPeriodicTaskJitter, func(ctx Ctx) error {
			return wsrv.withConn(ctx, func(conn StorageConn) error {
				if err := conn.CleanupSessions(wsrv.sessions); err != nil {
					wsrv.logger.Errorf("error when cleaning up sessions: %v", err)
				}
				return nil
			})
		}))

	}

	return tasks.Wait().ToError()
}

/************
Init helpers
************/

func (wsrv *WebServer) loginEnabled() bool {
	return wsrv.Discord.ClientID != "" && wsrv.Discord.ClientSecret != ""
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

func (wsrv *WebServer) initDBPool(ctx Ctx) (SQLiteConnPool, error) {
	pool, err := NewSQLiteConnPool(ctx, wsrv.NbDBConn, func(ctx Ctx) (SQLiteConn, error) {
		conn, err := wsrv.storage.GetConn(ctx)
		if err != nil {
			return conn, err
		}
		if wsrv.DirtyReads {
			return conn, conn.ReadUncommitted(true)
		}
		return conn, nil
	})
	if wsrv.DirtyReads && err == nil {
		wsrv.logger.Info("dirty reads of the database enabled")
	}
	return pool, nil
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
		listener = tls.NewListener(listener, wsrv.tls)
		wsrv.logger.Infof("TLS enabled on %s", wsrv.Listen)
		defer listener.Close()
	}

	wsrv.logger.Infof("listening on %s", wsrv.SelfLink)

	return IgnoreHTTPServerCloseErr(wsrv.server.Serve(listener))
}

/****************
Request handlers
****************/

// CSS serves the style sheets.
func (wsrv *WebServer) CSS(w http.ResponseWriter, r *http.Request) *HTTPError {
	var css string
	switch r.URL.Path {
	case "/css/main":
		css = CSSMain
	case "/css/reports":
		css = CSSReports
	case "/css/compendium":
		css = CSSCompendium
	default:
		return NewHTTPErrorf(http.StatusNotFound, "stylesheet %q doesn't exist", r.URL.Path)
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(css))
	return nil
}

// ReportIndex serves the reports' index (unimplemented).
func (wsrv *WebServer) ReportIndex(w http.ResponseWriter, r *http.Request) *HTTPError {
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
	return nil
}

// ReportSource serves the reports in markdown format according to the year and week in the URL.
func (wsrv *WebServer) ReportSource(w http.ResponseWriter, r *http.Request) *HTTPError {
	week, year, err := weekAndYear(NormalizeTrailing(HTTPRequestSubPath("/reports/source/", r)))
	if err != nil {
		return NewHTTPError(http.StatusBadRequest, err)
	}

	var report Report
	err = wsrv.withConn(r.Context(), func(conn StorageConn) error {
		var err error
		report, err = wsrv.reports.ReportWeek(conn, week, year)
		return err
	})
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, err)
	} else if report.Len() == 0 {
		return NewHTTPErrorf(http.StatusNotFound, "empty report for %d/%d", year, week)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := MarkdownReport.Execute(w, report); err != nil {
		panic(err)
	}
	return nil
}

// ReportStats serves an HTML document of the statistics for the year and week in the URL.
func (wsrv *WebServer) ReportStats(w http.ResponseWriter, r *http.Request) *HTTPError {
	week, year, err := weekAndYear(NormalizeTrailing(HTTPRequestSubPath("/reports/stats/", r)))
	if err != nil {
		return NewHTTPError(http.StatusBadRequest, err)
	}

	var data ReportHeader
	err = wsrv.withConn(r.Context(), func(conn StorageConn) error {
		var err error
		data, err = wsrv.reports.StatsWeek(conn, week, year)
		return err
	})
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, err)
	} else if data.Len == 0 {
		return NewHTTPErrorf(http.StatusNotFound, "no statistics for %d/%d", year, week)
	}

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "ReportStats", data); err != nil {
		panic(err)
	}
	return nil
}

// Report serves the HTML reports according to the year and week in the URL.
func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) *HTTPError {
	week, year, err := weekAndYear(NormalizeTrailing(HTTPRequestSubPath("/reports/", r)))
	if err != nil {
		return NewHTTPError(http.StatusBadRequest, err)
	}

	var report Report
	err = wsrv.withConn(r.Context(), func(conn StorageConn) error {
		var err error
		report, err = wsrv.reports.ReportWeek(conn, week, year)
		return err
	})
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, err)
	} else if report.Len() == 0 {
		return NewHTTPErrorf(http.StatusNotFound, "empty report for %d/%d", year, week)
	}

	report.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "Report", report); err != nil {
		panic(err)
	}
	return nil
}

// ReportCurrent redirects to the report for the current week.
func (wsrv *WebServer) ReportCurrent(w http.ResponseWriter, r *http.Request) *HTTPError {
	week, year := wsrv.reports.CurrentWeekCoordinates()
	return redirectToReport(week, year, w, r)
}

// ReportLatest redirects to the report for the previous week.
func (wsrv *WebServer) ReportLatest(w http.ResponseWriter, r *http.Request) *HTTPError {
	week, year := wsrv.reports.LastWeekCoordinates()
	return redirectToReport(week, year, w, r)
}

// CompendiumIndex serves the compendium's index.
func (wsrv *WebServer) CompendiumIndex(w http.ResponseWriter, r *http.Request) *HTTPError {
	var compendium Compendium
	err := wsrv.withConn(r.Context(), func(conn StorageConn) error {
		var err error
		compendium, err = wsrv.compendium.Index(conn)
		return err
	})
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, err)
	}

	compendium.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "Compendium", compendium); err != nil {
		panic(err)
	}
	return nil
}

// CompendiumUser serves the compendium page for a single user, whose name is taken from the URL (case-insensitive).
func (wsrv *WebServer) CompendiumUser(w http.ResponseWriter, r *http.Request) *HTTPError {
	args := NormalizeTrailing(HTTPRequestSubPath("/compendium/user/", r))
	if len(args) != 1 {
		msg := "invalid URL %s, use \"/compendium/user/username\" to view the page about \"username\""
		return NewHTTPErrorf(http.StatusBadRequest, msg, r.URL)
	}

	username := args[0]
	var stats CompendiumUser

	err := wsrv.withConn(r.Context(), func(conn StorageConn) error {
		var err error
		stats, err = wsrv.compendium.User(conn, username)
		if err != nil {
			return NewHTTPError(http.StatusInternalServerError, err)
		} else if !stats.Exists() {
			return NewHTTPErrorf(http.StatusNotFound, "user %q doesn't exist", username)
		}
		return nil
	})
	if err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, err)
	}

	stats.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "CompendiumUser", stats); err != nil {
		panic(err)
	}

	return nil
}

// CompendiumUserComments serves the comments of a user.
func (wsrv *WebServer) CompendiumUserComments(w http.ResponseWriter, r *http.Request) *HTTPError {
	args := NormalizeTrailing(HTTPRequestSubPath("/compendium/comments/user/", r))
	if len(args) != 1 {
		msg := "invalid URL %s, use \"/compendium/comments/user/username\" to view the comments of \"username\""
		return NewHTTPErrorf(http.StatusBadRequest, msg, r.URL)
	}
	username := args[0]

	page, err := wsrv.pagination(r.URL.Query())
	if err != nil {
		return NewHTTPError(http.StatusBadRequest, err)
	}

	var comments CompendiumUser

	err = wsrv.withConn(r.Context(), func(conn StorageConn) error {
		var err error
		comments, err = wsrv.compendium.UserComments(conn, username, page)
		if err != nil {
			return NewHTTPError(http.StatusInternalServerError, err)
		} else if !comments.Exists() {
			return NewHTTPErrorf(http.StatusNotFound, "user %q doesn't exist", username)
		}
		return nil
	})
	if err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, err)
	}

	comments.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "CompendiumUserComments", comments); err != nil {
		panic(err)
	}
	return nil
}

// CompendiumComments serves the paginated HTML document of all known comments from non-hidden users.
func (wsrv *WebServer) CompendiumComments(w http.ResponseWriter, r *http.Request) *HTTPError {
	page, err := wsrv.pagination(r.URL.Query())
	if err != nil {
		return NewHTTPError(http.StatusBadRequest, err)
	}

	var comments Compendium
	err = wsrv.withConn(r.Context(), func(conn StorageConn) error {
		var err error
		comments, err = wsrv.compendium.Comments(conn, page)
		if err != nil {
			return NewHTTPError(http.StatusInternalServerError, err)
		}
		return nil
	})
	if err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, err)
	}

	comments.CommentBodyConverter = wsrv.commentBodyConverter

	w.Header().Set("Content-Type", "text/html")
	if err := HTMLTemplates.ExecuteTemplate(w, "CompendiumComments", comments); err != nil {
		panic(err)
	}
	return nil
}

// BackupMain triggers a backup of the main database if needed, and serves it.
func (wsrv *WebServer) BackupMain(w http.ResponseWriter, r *http.Request) *HTTPError {
	err := wsrv.withConn(r.Context(), func(conn StorageConn) error {
		return wsrv.storage.BackupMain(r.Context(), conn)
	})
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, err)
	}
	w.Header().Set("Content-Type", "application/x-sqlite3")
	w.Header().Set("Content-Disposition", "attachment; filename=\"dab.sqlite3\"")
	http.ServeFile(w, r, wsrv.storage.Backup.Main)
	return nil
}

// BackupSecrets triggers a backup of the secrets database if needed, but does not serve it.
func (wsrv *WebServer) BackupSecrets(w http.ResponseWriter, r *http.Request) *HTTPError {
	err := wsrv.withConn(r.Context(), func(conn StorageConn) error {
		return wsrv.storage.BackupSecrets(r.Context(), conn)
	})
	if err != nil {
		return NewHTTPError(http.StatusInternalServerError, err)
	}
	return nil
}

func (wsrv *WebServer) authorize(conn StorageConn, w http.ResponseWriter, r *http.Request) *HTTPError {
	var session *WebSession
	var hErr *HTTPError

	if session, hErr = wsrv.sessions.FromRequest(conn, w, r); hErr != nil {
		return hErr
	} else if session == nil {
		if session, hErr = wsrv.sessions.New(conn, w); hErr != nil {
			return hErr
		}
	}

	if expired, hErr := session.Expire(conn, w); hErr != nil {
		return hErr
	} else if expired {
		if session, hErr = wsrv.sessions.New(conn, w); hErr != nil {
			return hErr
		}
	}

	// Any non-empty ID is valid and authorized.
	if session.ID != "" {
		return nil
	}

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return NewHTTPErrorf(http.StatusBadRequest, "could not parse the query part of the URL %q: %v", r.URL, err)
	}

	conf := wsrv.oAuthConf

	csrf := query.Get("state")
	code := query.Get("code")

	if code == "" || csrf == "" {
		if hErr = session.NewCSRF(conn, w); hErr != nil {
			return hErr
		}
		conf.RedirectURL = r.URL.String()
		http.Redirect(w, r, conf.AuthCodeURL(session.CSRF, oauth2.AccessTypeOnline), http.StatusSeeOther)
		return nil
	}

	if session.CSRF != csrf {
		return NewHTTPErrorf(http.StatusForbidden, "invalid CSRF token %q", csrf)
	}

	oAuthToken, err := conf.Exchange(r.Context(), code, oauth2.AccessTypeOnline)
	if err != nil {
		return NewHTTPErrorf(http.StatusServiceUnavailable, "failed to get an oAuth2 token with Discord: %v", err)
	}

	id, hErr := DiscordOAuthGetID(conf.Client(r.Context(), oAuthToken))
	if hErr != nil {
		return hErr
	}

	return session.SetID(conn, w, id)
}

/*************************
Request handlers' helpers
*************************/

func (wsrv *WebServer) withConn(ctx Ctx, cb func(StorageConn) error) error {
	return wsrv.conns.WithConn(ctx, func(conn SQLiteConn) error { return cb(conn.(StorageConn)) })
}

func (wsrv *WebServer) commentBodyConverter(src CommentView) (Any, error) {
	// We replace < and > with look-alikes because blackfriday's HTML renderer is poorly configurable,
	// and writing a replacement would be a timesink considering the original isn't very straightforward.
	body := matchTags.ReplaceAllString(src.Body, "\u2329$1\u232a")
	html := blackfriday.Run([]byte(body), markdownOptions)
	return template.HTML(html), nil
}

func redirectToReport(week uint8, year int, w http.ResponseWriter, r *http.Request) *HTTPError {
	http.Redirect(w, r, fmt.Sprintf("/reports/%d/%d", year, week), http.StatusTemporaryRedirect)
	return nil
}

func (wsrv *WebServer) immutableCache(handler HTTPAutoHandler) HTTPAutoHandler {
	return func(w http.ResponseWriter, r *http.Request) *HTTPError {
		requestedVersion := r.URL.Query().Get("version")
		// leave the empty version as a special case to easily link the object from a custom HTML file without having to constantly update it.
		if requestedVersion != "" {
			if requestedVersion != Version.String() {
				msg := "current version is %q, file for version %q is unavailable"
				return NewHTTPErrorf(http.StatusNotFound, msg, Version, requestedVersion)
			}

			if r.Header.Get("If-Modified-Since") != "" {
				w.WriteHeader(http.StatusNotModified)
				return nil
			}
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", HTTPCacheMaxAge))
		}

		return handler(w, r)
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
