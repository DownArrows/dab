package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"gopkg.in/russross/blackfriday.v2"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type WebServer struct {
	Server          *http.Server
	Reports         ReportFactory
	markdownOptions blackfriday.Option
}

type WebServerError struct {
	Status int
	Error  error
}

func NewWebServer(listen string, reports ReportFactory) *WebServer {
	md_exts := blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis

	wsrv := &WebServer{
		Reports:         reports,
		markdownOptions: blackfriday.WithExtensions(blackfriday.Extensions(md_exts)),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/reports", wsrv.ReportIndex)
	mux.HandleFunc("/reports/", wsrv.Report)
	mux.HandleFunc("/reports/current", wsrv.ReportCurrent)
	mux.HandleFunc("/reports/lastweek", wsrv.ReportLatest)
	mux.HandleFunc("/reports/source/", wsrv.ReportSource)

	wsrv.Server = &http.Server{
		Addr:           listen,
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	return wsrv
}

func (wsrv *WebServer) Run() error {
	return wsrv.Server.ListenAndServe()
}

func (wsrv *WebServer) Close() error {
	return wsrv.Server.Close()
}

func (wsrv *WebServer) newError(status int, err error) *WebServerError {
	return &WebServerError{Status: status, Error: err}
}

func (wsrv *WebServer) processError(w http.ResponseWriter, err *WebServerError) {
	w.WriteHeader(err.Status)
	w.Write([]byte(fmt.Sprint(err.Error)))
}

func (wsrv *WebServer) ReportIndex(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (wsrv *WebServer) ReportSource(w http.ResponseWriter, r *http.Request) {
	if report := wsrv.getReportFromURL("/reports/source/", w, r); report.Len() != 0 {
		var output io.Writer = w

		w.Header().Set("Content-Type", "text/plain")

		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			output = gzip.NewWriter(w)
			defer output.(io.WriteCloser).Close()
		}

		autopanic(WriteMarkdownReport(report, output))
	}
}

func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) {
	report := wsrv.getReportFromURL("/reports/", w, r)
	if report.Len() == 0 {
		return
	}
	write_page := wsrv.prepareReportPage(report)
	write_page(w, r)
}

func (wsrv *WebServer) ReportCurrent(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.Reports.CurrentWeekCoordinates()
	report, err := wsrv.getReport(week, year)
	if err != nil {
		wsrv.processError(w, err)
		return
	}
	write_page := wsrv.prepareReportPage(report)
	write_page(w, r)
}

func (wsrv *WebServer) ReportLatest(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.Reports.LastWeekCoordinates()
	report, err := wsrv.getReport(week, year)
	if err != nil {
		wsrv.processError(w, err)
		return
	}
	write_page := wsrv.prepareReportPage(report)
	write_page(w, r)
}

func (wsrv *WebServer) getReportFromURL(prefix string, w http.ResponseWriter, r *http.Request) Report {
	var report Report
	week, year, ws_err := wsrv.getWeekYearFromURL(r, prefix)
	if ws_err != nil {
		wsrv.processError(w, ws_err)
		return report
	}
	report, ws_err = wsrv.getReport(week, year)
	if ws_err != nil {
		wsrv.processError(w, ws_err)
		return report
	}
	return report
}

type HTMLReportComment struct {
	ReportComment
	HTMLBody template.HTML
}

func (wsrv *WebServer) prepareReportPage(report Report) func(http.ResponseWriter, *http.Request) {
	var comments []HTMLReportComment
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

	return func(w http.ResponseWriter, r *http.Request) {
		var output io.Writer = w

		w.Header().Set("Content-Type", "text/html")

		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			output = gzip.NewWriter(w)
			defer output.(io.WriteCloser).Close()
		}

		autopanic(HTMLReportPage.Execute(output, data))
	}
}

func (wsrv *WebServer) getReport(week uint8, year int) (Report, *WebServerError) {
	report := wsrv.Reports.ReportWeek(week, year)
	if report.Len() == 0 {
		return report, wsrv.newError(404, fmt.Errorf("No report for week %d year %d.", week, year))
	}
	return report, nil
}

func (wsrv *WebServer) getWeekYearFromURL(r *http.Request, leadingPath string) (uint8, int, *WebServerError) {
	path := subPath(leadingPath, r)
	week, year, err := weekAndYear(path)
	if err != nil {
		return 0, 0, wsrv.newError(400, err)
	}
	return week, year, nil
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
