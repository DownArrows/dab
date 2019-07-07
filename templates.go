package main

import (
	html "html/template"
	"io"
	"strings"
	text "text/template"
)

var MarkdownReportHead = autoTemplate("MarkdownHead", `
From {{.Start.Format "02 Jan 06 15:04 MST"}} to {{.End.Format "02 Jan 06 15:04 MST"}}.

Top {{.Delta | len}} total negative karma change for this week:
{{range .Delta}}
- **{{.Summary}}** with {{.Count}} posts,
by [/u/{{.Name}}](https://reddit.com/user/{{.Name}})
{{- end}}

Top {{.Average | len}} lowest average karma per comment:
{{range .Average}}
- **{{.Summary}}** with {{.Count}} posts,
by [/u/{{.Name}}](https://reddit.com/user/{{.Name}})
{{- end}}

* * *`)

var MarkdownReportComment = autoTemplate("MarkdownComment", `
# \#{{.Number}}

Author: [/u/{{.Author}}](https://reddit.com/user/{{.Author}}) ({{.Average}} week average)

Date: {{.Created.Format "Monday 02 January 15:04 PM"}}

Score: **{{.Score}}**

Subreddit: [/r/{{.Sub}}](https://reddit.com/r/{{.Sub}})

Link: [{{.Permalink}}](https://reddit.com{{.Permalink}})

Post text:

{{range .BodyLines}}
> {{.}}
{{end}}`)

var HTMLReportPage = autoHTMLTemplate("HTMLReportPage", `
<!DOCTYPE html>
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Report of year {{.Year}} week {{.Week}}</title>
	<style>
		:root {
			--main-color: #6a6;
			--sec-color: #5af;
			--bg: #eee;
			--fg: #555;
			--spacing: 0.2em;
		}

		body {
			max-width: 63rem;
			margin: 1rem auto;
			color: var(--fg);
			font-size: 0.75em;
		}

		@media (min-width: 40rem) {
			body { font-size: 1em }
		}

		body > h1, article > h1 {
			color: var(--main-color)
		}

		#title {
			text-align: center;
			font-size: 1.5em;
		}

		@media (min-width: 40rem) {
			#title {
				font-size: 2em;
				margin-bottom: 2em;
			}
		}

		aside {
			font-weight: bold;
			margin-right: 1em;
		}

		@media (min-width: 40rem) {
			aside { float: right }
		}

		aside a::after {
			content: "M↓";
			color: var(--bg);
			background: var(--fg);
			border-radius: var(--spacing);
			font-weight: bold;
			padding: calc(var(--spacing)/2);
			margin: calc(var(--spacing)/2);
			font-size: smaller;
		}

		#report-head {
			margin: var(--spacing);
		}

		#report-head h1, #report-head h2 {
			font-size: 1em;
		}

		@media (min-width: 30em) {
			#report-head h1 { font-size: 1.5em }
			#report-head h2 { font-size: 1.25em }
		}

		#report-head ol {
			list-style-type: none;
		}

		.comment .score {
			color: var(--main-color);
			font-weight: bold;
		}

		.comment > dl {
			display: table;
			border-spacing: calc(2 * var(--spacing));
		}

		.comment > dl > .ditem {
			display: table-row;
		}

		.ditem > dt, .ditem > dd {
			display: table-cell;
		}

		.comment > blockquote {
			overflow-wrap: break-word;
		}

		.comment > blockquote blockquote {
			background: var(--bg);
			border-left: solid var(--spacing) var(--main-color);
			margin-left: 0;
			padding: var(--spacing);
			padding-left: calc(2 * var(--spacing));
		}

		.comment > blockquote pre {
			overflow-x: scroll;
			padding: var(--spacing);
			border-radius: var(--spacing);
			background: #eee;
		}

		main {
			margin-bottom: 1em;
		}

		a {
			color: var(--sec-color);
			text-decoration: none;
		}

		a:hover {
			text-decoration: underline;
		}

		a:visited {
			color: #9bd;
			text-decoration: none;
		}

		footer a {
			display: block;
			text-align: center;
		}
	</style>
</head>
<body>
<h1 id="title">Report of year {{.Year}} week {{.Week}}</h1>

<aside><a href="/reports/source/{{.Year}}/{{.Week}}">source</a></aside>

<article id="report-head">
{{with .Head}}
	<h1>{{.Number}} comments under {{.CutOff}} from {{.Start.Format "02 Jan 06 15:04 MST"}} to {{.End.Format "02 Jan 06 15:04 MST"}}</h1>

	<h2>Top {{.Delta | len}} total negative karma change for this week</h2>
	<ol>
	{{range .Delta}}
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="https://reddit.com/user/{{.Name}}">/u/{{.Name}}</a></li>
	{{- end}}
	</ol>

	<h2>Top {{.Average | len}} lowest average karma per comment</h2>
	<ol>
	{{range .Average}}
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="https://reddit.com/user/{{.Name}}">/u/{{.Name}}</a></li>
	{{- end}}
	</ol>
{{- end}}
</article>
<hr/>

<main>
{{range .Comments}}
<article class="comment">
	<h1>#{{.Number}}</h1>

	<dl>

	<div class="ditem">
	<dt>Author</dt>
	<dd><a href="https://reddit.com/user/{{.Author}}">/u/{{.Author}}</a> ({{.Average}} week average)</dd>
	</div>

	<div class="ditem">
	<dt>Date</dt>
	<dd>{{.Created.Format "Monday 02 January 15:04 PM"}}</dd>
	</div>

	<div class="ditem">
	<dt>Score</dt>
	<dd class="score">{{.Score}}</dd>
	</div>

	<div class="ditem">
	<dt>Subreddit</dt>
	<dd><a href="https://reddit.com/r/{{.Sub}}">/r/{{.Sub}}</a></dd>
	</div>

	<div class="ditem">
	<dt>Link</dt>
	<dd><a href="https://reddit.com{{.Permalink}}">{{.Permalink}}</a></dd>
	</div>

	</dl>

	<blockquote>
	{{.HTMLBody}}
	</blockquote>
</article>
{{end}}
</main>
<footer><a href="#title">back to top</a></footer>
</body>`)

func autoTemplate(name, source string) *text.Template {
	return text.Must(text.New(name).Parse(strings.TrimPrefix(source, "\n")))
}

func autoHTMLTemplate(name, source string) *html.Template {
	return html.Must(html.New(name).Parse(strings.TrimPrefix(source, "\n")))
}

func WriteMarkdownReport(r Report, out io.Writer) error {
	sep := []byte("\n\n\n")
	if err := MarkdownReportHead.Execute(out, r.Head()); err != nil {
		return err
	}
	if _, err := out.Write(sep); err != nil {
		return err
	}
	for _, comment := range r.Comments() {
		if err := MarkdownReportComment.Execute(out, comment); err != nil {
			return err
		}
		if _, err := out.Write(sep); err != nil {
			return err
		}
	}
	return nil
}
