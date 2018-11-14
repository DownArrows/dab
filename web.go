package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"gopkg.in/russross/blackfriday.v2"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

type Response struct {
	Actual http.ResponseWriter
	Gzip   *gzip.Writer
}

func NewResponse(w http.ResponseWriter, r *http.Request) Response {
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		autopanic(err)
		return Response{Actual: w, Gzip: gw}
	}
	return Response{Actual: w}
}

func (r Response) Write(data []byte) (int, error) {
	if r.Gzip == nil {
		return r.Actual.Write(data)
	}
	return r.Gzip.Write(data)
}

func (r Response) Close() error {
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
	backupAuth      string
	backupStorage   BackupStorage
}

func NewWebServer(conf WebConf, reports ReportFactory, bs BackupStorage) *WebServer {
	md_exts := blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis

	wsrv := &WebServer{
		Reports:         reports,
		markdownOptions: blackfriday.WithExtensions(blackfriday.Extensions(md_exts)),
		backupAuth:      conf.BackupAuth,
		backupStorage:   bs,
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

func (wsrv *WebServer) Run() error {
	return wsrv.Server.ListenAndServe()
}

func (wsrv *WebServer) Close() error {
	return wsrv.Server.Close()
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
	output := NewResponse(w, r)
	defer output.Close()
	autopanic(WriteMarkdownReport(report, output))
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
	output := NewResponse(w, r)
	defer output.Close()
	autopanic(HTMLReportPage.Execute(output, data))
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	path := r.Form.Get("path")
	auth := r.Form.Get("auth")
	if path == "" || auth == "" {
		http.Error(w, "You must provide a 'path' and an 'auth' parameter", http.StatusBadRequest)
		return
	}
	if auth != wsrv.backupAuth {
		http.Error(w, "Invalid authentication token", http.StatusForbidden)
		return
	}
	if err := wsrv.backupStorage.Backup(path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func redirectToReport(week uint8, year int, w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, fmt.Sprintf("/reports/%d/%d", year, week), http.StatusTemporaryRedirect)
}

func subPath(prefix string, r *http.Request) []string {
	sub_url := r.URL.Path[len(prefix):]
	return strings.Split(sub_url, "/")
}

func ignoreTrailing(path []string) []string {
	if path[len(path)-1] == "" {
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
