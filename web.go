package main

import (
	"errors"
	"fmt"
	"gopkg.in/russross/blackfriday.v2"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type WebServer struct {
	Server          *http.Server
	reportsTmpl     *template.Template
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
		reportsTmpl:     template.Must(template.New("html_report").Parse(reportPage)),
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
	if source, _, _ := wsrv.getReportFromURL("/reports/source/", w, r); source != "" {
		w.Write([]byte(source))
	}
}

func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) {
	source, week, year := wsrv.getReportFromURL("/reports/", w, r)
	if source == "" {
		return
	}
	write_page := wsrv.prepareReportPage(source, week, year)
	write_page(w)
}

func (wsrv *WebServer) ReportCurrent(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.Reports.CurrentWeekCoordinates()
	source, err := wsrv.getReport(week, year)
	if err != nil {
		wsrv.processError(w, err)
		return
	}
	write_page := wsrv.prepareReportPage(source, week, year)
	write_page(w)
}

func (wsrv *WebServer) ReportLatest(w http.ResponseWriter, r *http.Request) {
	week, year := wsrv.Reports.LastWeekCoordinates()
	source, err := wsrv.getReport(week, year)
	if err != nil {
		wsrv.processError(w, err)
		return
	}
	write_page := wsrv.prepareReportPage(source, week, year)
	write_page(w)
}

func (wsrv *WebServer) getReportFromURL(prefix string, w http.ResponseWriter, r *http.Request) (string, uint8, int) {
	week, year, ws_err := wsrv.getWeekYearFromURL(r, prefix)
	if ws_err != nil {
		wsrv.processError(w, ws_err)
		return "", 0, 0
	}
	source, ws_err := wsrv.getReport(week, year)
	if ws_err != nil {
		wsrv.processError(w, ws_err)
		return "", 0, 0
	}
	return source, week, year
}

func (wsrv *WebServer) prepareReportPage(source string, week uint8, year int) func(http.ResponseWriter) {
	content := blackfriday.Run([]byte(source), wsrv.markdownOptions)
	data := map[string]interface{}{
		"Title":   fmt.Sprintf("Report of year %d week %d", year, week),
		"Content": template.HTML(content),
		"Year":    year,
		"Week":    week,
	}
	return func(w http.ResponseWriter) { wsrv.reportsTmpl.Execute(w, data) }
}

func (wsrv *WebServer) getReport(week uint8, year int) (string, *WebServerError) {
	report := wsrv.Reports.ReportWeek(week, year)
	if report.Len() == 0 {
		return "", wsrv.newError(404, fmt.Errorf("No report for week %d year %d.", week, year))
	}
	return fmt.Sprint(report), nil
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

const reportPage = `<!DOCTYPE html>
<head>
	<meta charset="utf-8"/>
	<title>{{ .Title }}</title>
	<style>
		body {
			max-width: 63rem;
			margin: 1rem auto;
			color: #555;
		}

		blockquote blockquote {
			background: #eee;
			border-left: solid 5px #aaa;
			margin-left: 0;
			padding: 0.2em;
			padding-left: 0.4em;
		}

		h1 { color: #6a6 }

		#title {
			text-align: center;
			font-size: 2rem;
			margin-bottom: 2rem;
		}

		aside {
			float: right;
			font-weight: bold;
			margin-right: 1em;
		}

		aside a::after {
			content: "M↓";
			color: white;
			background: black;
			border-radius: 0.2em;
			font-weight: bold;
			padding: 0.1em;
			margin: 0.1em;
			font-size: smaller;
		}

		article h1 {
			font-size: 1.25rem;
		}

		a {
			color: #5af;
			text-decoration: none;
		}

		a:hover {
			text-decoration: underline;
		}

		a:visited {
			color: #9bd;
			text-decoration: none;
		}
	</style>
</head>
<body>
	<h1 id="title">{{ .Title }}</h1>
	<aside><a href="/reports/source/{{ .Year }}/{{ .Week }}">source</a></aside>
	<article>
		{{ .Content }}
	</article>
</body>`
