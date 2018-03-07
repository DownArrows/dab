package main

import (
	"fmt"
	"gopkg.in/russross/blackfriday.v2"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func MakeReportHandler(prefix string, typer *ReportTyper) http.HandlerFunc {
	tmpl := template.Must(template.New("html_report").Parse(reportPage))
	opts := blackfriday.Extensions(blackfriday.Tables | blackfriday.Autolink | blackfriday.Strikethrough | blackfriday.NoIntraEmphasis)

	return func(w http.ResponseWriter, r *http.Request) {
		log.Print(r.URL.Path)
		sub_url := r.URL.Path[len(prefix):]
		path := strings.Split(sub_url, "/")

		if path[len(path)-1] == "" {
			path = path[:len(path)-2]
		}

		if len(path) != 2 {
			http.NotFound(w, r)
			return
		}

		wants_source := false

		if strings.HasSuffix(path[1], ".txt") {
			wants_source = true
			path[1] = strings.TrimSuffix(path[1], ".txt")
		}

		year, err := strconv.Atoi(path[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}

		week, err := strconv.Atoi(path[1])
		if err != nil {
			http.NotFound(w, r)
			return
		}

		report, err := typer.ReportWeek(uint8(week), year)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		source := []byte(strings.Join(report, ""))

		if wants_source {
			w.Write(source)
		} else {
			content := blackfriday.Run(source, blackfriday.WithExtensions(opts))
			tmpl.Execute(w, map[string]interface{}{
				"Title":   fmt.Sprintf("Report of year %d week %d", year, week),
				"Content": template.HTML(string(content)),
			})
		}
	}
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
	<article>
		{{ .Content }}
	</article>
</body>`
