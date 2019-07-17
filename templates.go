package main

import (
	html "html/template"
	text "text/template"
)

var MarkdownReport = text.Must(text.New("MarkdownReport").Parse(`
{{- with .Head -}}
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
{{- end}}

* * *

{{range .Comments -}}
# \#{{.Number}}

Author: [/u/{{.Author}}](https://reddit.com/user/{{.Author}}) ({{.Average}} week average)

Date: {{.Created.Format "Monday 02 January 15:04 PM"}}

Score: **{{.Score}}**

Subreddit: [/r/{{.Sub}}](https://reddit.com/r/{{.Sub}})

Link: [{{.Permalink}}](https://reddit.com{{.Permalink}})

Post text:

{{range .BodyLines -}}
> {{.}}
{{end}}
{{end -}}
`))

var HTMLReportPage = html.Must(html.New("HTMLReportPage").Parse(`<!DOCTYPE html>
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Report of year {{.Year}} week {{.Week}}</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
	<link rel="stylesheet" href="/css/reports?version={{.Version}}">
</head>
<body>
<h1 id="title">Report of year {{.Year}} week {{.Week}}</h1>

<aside class="md-link"><a href="/reports/source/{{.Year}}/{{.Week}}">source</a></aside>

<article id="report-head">
{{- with .Head}}
	<h1>{{.Number}} comments under {{.CutOff}} from {{.Start.Format "02 Jan 06 15:04 MST"}} to {{.End.Format "02 Jan 06 15:04 MST"}}</h1>

	<h2>Top {{.Delta | len}} total negative karma change for this week</h2>
	<ol>
	{{- range .Delta}}
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="https://reddit.com/user/{{.Name}}">/u/{{.Name}}</a></li>
	{{- end}}
	</ol>

	<h2>Top {{.Average | len}} lowest average karma per comment</h2>
	<ol>
	{{- range .Average}}
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="https://reddit.com/user/{{.Name}}">/u/{{.Name}}</a></li>
	{{- end}}
	</ol>
{{- end -}}
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
{{.BodyConvert}}
	</blockquote>
</article>
{{end}}
</main>

<footer><a href="#title">back to top</a></footer>

</body>`))

const CSSMain = `:root {
	--main-color: #6a6;
	--sec-color: #5af;
	--bg: #eee;
	--fg: #555;
	--spacing: 0.2em;
}

body {
	color: var(--fg);
	font-size: 0.75em;
}

@media (min-width: 40rem) {
	body { font-size: 1em }
}

body > h1, article > h1 {
	color: var(--main-color)
}

.ditem > dt, .ditem > dd {
	display: table-cell;
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
}`

const CSSReports = `body {
	max-width: 63rem;
	margin: 1rem auto;
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

aside.md-link {
	font-weight: bold;
	margin-right: 1em;
}

@media (min-width: 40rem) {
	aside.md-link { float: right }
}

aside.md-link a::after {
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

footer a {
	display: block;
	text-align: center;
}`
