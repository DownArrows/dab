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

// Simple hack to get compression without having to create a whole type implementing http.ResponseWriter
type webResponse struct {
	Actual http.ResponseWriter
	Gzip   *gzip.Writer
}

func newWebResponse(w http.ResponseWriter, r *http.Request) webResponse {
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			panic(err)
		}
		return webResponse{Actual: w, Gzip: gw}
	}
	return webResponse{Actual: w}
}

func (r webResponse) Write(data []byte) (int, error) {
	if r.Gzip == nil {
		return r.Actual.Write(data)
	}
	return r.Gzip.Write(data)
}

func (r webResponse) Close() error {
	if r.Gzip == nil {
		return nil
	}
	return r.Gzip.Close()
}

// Component
type WebServer struct {
	backupStorage   BackupStorage
	done            chan error
	markdownOptions blackfriday.Option
	reports         ReportFactory
	server          *http.Server
}

func NewWebServer(conf WebConf, reports ReportFactory, bs BackupStorage) *WebServer {
	md_exts := blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis

	wsrv := &WebServer{
		reports:         reports,
		markdownOptions: blackfriday.WithExtensions(blackfriday.Extensions(md_exts)),
		backupStorage:   bs,
		done:            make(chan error),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/reports", wsrv.ReportIndex)
	mux.HandleFunc("/reports/", wsrv.Report)
	mux.HandleFunc("/reports/current", wsrv.ReportCurrent)
	mux.HandleFunc("/reports/lastweek", wsrv.ReportLatest)
	mux.HandleFunc("/reports/source/", wsrv.ReportSource)
	mux.HandleFunc("/backup", wsrv.Backup)

	wsrv.server = &http.Server{Addr: conf.Listen, Handler: mux}

	return wsrv
}

func (wsrv *WebServer) fatal(err error) {
	wsrv.done <- err
}

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

func (wsrv *WebServer) ReportIndex(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (wsrv *WebServer) ReportSource(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/source/", r)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	report := wsrv.reports.ReportWeek(week, year)
	if report.Len() == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	output := newWebResponse(w, r)
	defer output.Close()
	if err := MarkdownReport.Execute(output, report); err != nil {
		wsrv.fatal(err)
	}
}

func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/", r)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report := wsrv.reports.ReportWeek(week, year)
	if report.Len() == 0 {
		http.NotFound(w, r)
		return
	}

	report.CommentBodyConverter = func(src ReportComment) (interface{}, error) {
		html := blackfriday.Run([]byte(src.Body), wsrv.markdownOptions)
		return template.HTML(html), nil
	}
	w.Header().Set("Content-Type", "text/html")
	output := newWebResponse(w, r)
	defer output.Close()
	if err := HTMLReportPage.Execute(output, report); err != nil {
		wsrv.fatal(err)
	}
}

func (wsrv *WebServer) ReportCurrent(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.reports.CurrentWeekCoordinates()
	redirectToReport(week, year, w, r)
}

func (wsrv *WebServer) ReportLatest(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.reports.LastWeekCoordinates()
	redirectToReport(week, year, w, r)
}

func (wsrv *WebServer) Backup(w http.ResponseWriter, r *http.Request) {
	if err := wsrv.backupStorage.Backup(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-sqlite3")
	http.ServeFile(w, r, wsrv.backupStorage.BackupPath())
}

func redirectToReport(week uint8, year int, w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, fmt.Sprintf("/reports/%d/%d", year, week), http.StatusTemporaryRedirect)
}

func subPath(prefix string, r *http.Request) []string {
	sub_url := r.URL.Path[len(prefix):]
	return strings.Split(sub_url, "/")
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
