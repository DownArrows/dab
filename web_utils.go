package main

import (
	"compress/gzip"
	"context"
	"fmt"
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
	// WebServerListenFDRedirector is the file descriptor number onto which the HTTPS redirector will run
	// if activation basde on file descriptor is enabled.
	WebServerListenFDRedirector = 4
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
	tasks.SpawnCtx(HTTPServerShutdown(rdr.server))
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

	return IgnoreHTTPServerCloseErr(rdr.server.Serve(listener))
}

func (rdr *Redirector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rdr.logger.Infod(func() interface{} {
		return fmt.Sprintf("ignoring %s %s from %s with user agent %q and force redirect to https",
			r.Method, r.URL, getIP(r, rdr.IPHeader), r.Header.Get("User-Agent"))
	})
	http.Redirect(w, r, rdr.Target, http.StatusSeeOther)
}
