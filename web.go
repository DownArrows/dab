package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"gopkg.in/russross/blackfriday.v2"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// Simple hack to get compression without having to create a whole type implemeting http.ResponseWriter
type webResponse struct {
	Actual http.ResponseWriter
	Gzip   *gzip.Writer
}

func newWebResponse(w http.ResponseWriter, r *http.Request) webResponse {
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		autopanic(err)
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

type HTMLReportComment struct {
	ReportComment
	HTMLBody template.HTML
}

type WebServer struct {
	Server          *http.Server
	Reports         ReportFactory
	markdownOptions blackfriday.Option
	backupStorage   BackupStorage
	done            chan error
}

func NewWebServer(conf WebConf, reports ReportFactory, bs BackupStorage) *WebServer {
	md_exts := blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis

	wsrv := &WebServer{
		Reports:         reports,
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

	wsrv.Server = &http.Server{Addr: conf.Listen, Handler: mux}

	return wsrv
}

func (wsrv *WebServer) fatal(err error) {
	wsrv.done <- err
}

func (wsrv *WebServer) Run(ctx context.Context) error {
	go func() {
		wsrv.done <- wsrv.Server.ListenAndServe()
	}()

	var err error
	select {
	case <-ctx.Done():
		wsrv.Server.Close()
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

	report := wsrv.Reports.ReportWeek(week, year)
	if report.Len() == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	output := newWebResponse(w, r)
	defer output.Close()
	if err := WriteMarkdownReport(report, output); err != nil {
		wsrv.fatal(err)
	}
}

func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) {
	week, year, err := weekAndYear(ignoreTrailing(subPath("/reports/", r)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report := wsrv.Reports.ReportWeek(week, year)
	if report.Len() == 0 {
		http.NotFound(w, r)
		return
	}

	comments := make([]HTMLReportComment, 0, report.Len())
	for _, src := range report.Comments() {
		var comment HTMLReportComment
		comment.ReportComment = src
		html := blackfriday.Run([]byte(src.Body), wsrv.markdownOptions)
		comment.HTMLBody = template.HTML(html)
		comments = append(comments, comment)
	}
	data := map[string]interface{}{
		"Year":     report.Year,
		"Week":     report.Week,
		"Head":     report.Head(),
		"Comments": comments,
	}

	w.Header().Set("Content-Type", "text/html")
	output := newWebResponse(w, r)
	defer output.Close()
	if err := HTMLReportPage.Execute(output, data); err != nil {
		wsrv.fatal(err)
	}
}

func (wsrv *WebServer) ReportCurrent(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.Reports.CurrentWeekCoordinates()
	redirectToReport(week, year, w, r)
}

func (wsrv *WebServer) ReportLatest(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.Reports.LastWeekCoordinates()
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
