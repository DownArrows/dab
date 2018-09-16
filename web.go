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
	Server      *http.Server
	reportsTmpl *template.Template
	Typer       *ReportTyper
}

func NewWebServer(listen string, typer *ReportTyper) *WebServer {
	reports_tmpl := template.Must(template.New("html_report").Parse(reportPage))

	wsrv := &WebServer{
		reportsTmpl: reports_tmpl,
		Typer:       typer,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/reports", wsrv.ReportIndex)
	mux.HandleFunc("/reports/", wsrv.Report)
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

func (wsrv *WebServer) ReportIndex(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (wsrv *WebServer) Report(w http.ResponseWriter, r *http.Request) {
	year, week, source, err := wsrv.getReportFromURL("/reports/", w, r)
	if err != nil {
		return
	}

	opts := blackfriday.Extensions(blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis)
	content := blackfriday.Run(source, blackfriday.WithExtensions(opts))
	wsrv.reportsTmpl.Execute(w, map[string]interface{}{
		"Title":   fmt.Sprintf("Report of year %d week %d", year, week),
		"Content": template.HTML(string(content)),
		"Year":    year,
		"Week":    week,
	})
}

func (wsrv *WebServer) ReportSource(w http.ResponseWriter, r *http.Request) {
	if _, _, report, err := wsrv.getReportFromURL("/reports/source/", w, r); err == nil {
		w.Write(report)
	}
}

func (wsrv *WebServer) getReportFromURL(leadingPath string, w http.ResponseWriter, r *http.Request) (int, int, []byte, error) {
	path := subPath(leadingPath, r)

	year, week, err := yearAndWeek(path)
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte(fmt.Sprint(err)))
		return 0, 0, nil, err
	}

	report, err := wsrv.Typer.ReportWeek(uint8(week), year)
	if err != nil {
		http.NotFound(w, r)
		return 0, 0, nil, err
	}

	return year, week, []byte(strings.Join(report, "")), nil
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

func yearAndWeek(path []string) (int, int, error) {
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

	return year, week, nil
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
