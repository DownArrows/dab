package main

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"golang.org/x/crypto/acme/autocert"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// HTTPCacheMaxAge is the maxmimum cache age one can set in response to an HTTP request.
const HTTPCacheMaxAge = 31536000

const (
	// WebServerListenFD is the file descriptor number onto which the web server will serve its content
	// if activation basde on file descriptor is enabled.
	WebServerListenFD = 3
	// WebServerListenFDHelper is the file descriptor number onto which the HTTPS redirector will run
	// if activation based on file descriptor is enabled.
	WebServerListenFDHelper = 4
)

// IgnoreHTTPServerCloseErr filters out the uninformative http.ErrServerClosed.
func IgnoreHTTPServerCloseErr(err error) error {
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// HTTPServerShutdown creates a Task from an http.Server that shuts it down when the task is cancelled.
func HTTPServerShutdown(srv *http.Server) Task {
	return func(ctx Ctx) error {
		<-ctx.Done()
		return srv.Shutdown(context.Background())
	}
}

// HTTPRequestSubPath returns the path after a prefix in an *http.Requets.
func HTTPRequestSubPath(prefix string, r *http.Request) []string {
	subURL := r.URL.Path[len(prefix):]
	return strings.Split(subURL, "/")
}

// NormalizeTrailing removes the empty string at the end of an already split path with a trailing separator.
func NormalizeTrailing(path []string) []string {
	if len(path) >= 2 && path[len(path)-1] == "" {
		path = path[:len(path)-2]
	}
	return path
}

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

// TLSHelper is a simple HTTP server that redirects to another, secure, URL,
// and helps with ACME HTTP challenge.
type TLSHelper struct {
	TLSHelperConf
	logger   LevelLogger
	SelfLink string
	server   *http.Server
}

// NewTLSHelper creates a new HTTPS helper, with an optional autocert manager to deal with ACME challenges.
func NewTLSHelper(logger LevelLogger, am *ACMEManager, conf TLSHelperConf) (*TLSHelper, error) {
	var err error

	th := &TLSHelper{
		TLSHelperConf: conf,
		logger:        logger,
		server:        &http.Server{Addr: conf.Listen},
	}
	th.SelfLink, err = th.getSelfLink()
	if err != nil {
		return nil, err
	}
	if am != nil {
		th.server.Handler = am.HTTPHandler(http.HandlerFunc(th.redirect))
	} else {
		th.server.Handler = http.HandlerFunc(th.redirect)
	}
	return th, nil
}

func (th *TLSHelper) getSelfLink() (string, error) {
	if th.ListenFDs > 0 {
		return fmt.Sprintf("file descriptor %d", WebServerListenFDHelper), nil
	}
	selfLink, err := URLFromHostPort(th.Listen)
	if err != nil {
		return "", err
	}
	selfLink.Scheme = "http"
	return selfLink.String(), nil
}

// Run is a Task that blocks until the context is cancelled, thereby shutting down the TLS helper.
func (th *TLSHelper) Run(ctx Ctx) error {
	tasks := NewTaskGroup(ctx)
	tasks.SpawnCtx(func(_ Ctx) error { return th.listen() })
	tasks.SpawnCtx(HTTPServerShutdown(th.server))
	th.logger.Infof("redirector listening on %s", th.SelfLink)
	return tasks.Wait().ToError()
}

func (th *TLSHelper) listen() error {
	var err error
	var listener net.Listener

	if th.ListenFDs > 0 {
		fd := os.NewFile(WebServerListenFDHelper, "redirector")
		defer fd.Close()
		listener, err = net.FileListener(fd)
		if err != nil {
			msg := "error with the redirector's file descriptor %d when trying to create a listener on it: %v"
			return fmt.Errorf(msg, WebServerListenFDHelper, err)
		}
	} else {
		listener, err = net.Listen("tcp", th.Listen)
	}
	defer listener.Close()

	return IgnoreHTTPServerCloseErr(th.server.Serve(listener))
}

func (th *TLSHelper) redirect(w http.ResponseWriter, r *http.Request) {
	th.logger.Infod(func() interface{} {
		return fmt.Sprintf("ignoring %s %s from %s with user agent %q and force redirect to https",
			r.Method, r.URL, getIP(r, th.IPHeader), r.Header.Get("User-Agent"))
	})
	http.Redirect(w, r, th.Target, http.StatusSeeOther)
}

// ACMEManager manages automatic TLS certificates.
// Wraps autocert.Manager for ease of use and swapping of implementation.
type ACMEManager struct {
	manager   *autocert.Manager
	tlsConfig *tls.Config
}

// NewACMEManager creates a new ACMEManager using a cache from a database.
func NewACMEManager(pool SQLiteConnPool, hosts ...string) (*ACMEManager, error) {
	mngr := &autocert.Manager{
		Cache:      &CertCache{pool: pool},
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(hosts...),
	}
	am := &ACMEManager{
		manager:   mngr,
		tlsConfig: mngr.TLSConfig(),
	}
	return am, nil
}

// HTTPHandler returns an HTTP handler for ACME challenges.
func (am *ACMEManager) HTTPHandler(fallback http.Handler) http.Handler {
	return am.manager.HTTPHandler(fallback)
}

// TLSNextProtos returns the compatible NextProtos TLS configuration values (for use in tls.Config).
func (am *ACMEManager) TLSNextProtos() []string {
	return am.tlsConfig.NextProtos
}

// GetCertificate gets the certificate information for a client (for use in tls.Config)
func (am *ACMEManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return am.manager.GetCertificate(hello)
}

// CertCache is a cache for autocert.Manager.
type CertCache struct {
	pool SQLiteConnPool
}

// InitializationQueries returns SQL queries to store certificates that assume a "secret" database exists.
func (cc CertCache) InitializationQueries() []SQLQuery {
	return []SQLQuery{
		{SQL: `CREATE TABLE IF NOT EXISTS secrets.certs (
			key TEXT PRIMARY KEY,
			cert BLOB NOT NULL
		) WITHOUT ROWID`},
	}
}

// Get implements autocert.Cache.
func (cc CertCache) Get(ctx Ctx, key string) ([]byte, error) {
	var err error
	var cert []byte
	query := "SELECT cert FROM certs WHERE key = ?"
	err = cc.pool.WithConn(ctx, func(conn SQLiteConn) error {
		return conn.Select(query, func(stmt *SQLiteStmt) error {
			cert, _, err = stmt.ColumnBlob(0)
			return err
		}, key)
	})
	if cert == nil {
		return nil, autocert.ErrCacheMiss
	}
	return cert, nil
}

// Put implements autocert.Cache.
func (cc CertCache) Put(ctx Ctx, key string, cert []byte) error {
	return cc.pool.WithConn(ctx, func(conn SQLiteConn) error {
		return conn.Exec("INSERT INTO certs VALUES (?, ?)", key, cert)
	})
}

// Delete implements autocert.Cache.
func (cc CertCache) Delete(ctx Ctx, key string) error {
	return cc.pool.WithConn(ctx, func(conn SQLiteConn) error {
		return conn.Exec("DELETE FROM certs WHERE key = ?", key)
	})
}
