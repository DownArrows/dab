package main

import (
	html "html/template"
	text "text/template"
)

var MarkdownReport = text.Must(text.New("MarkdownReport").Parse(`
{{- with .Head -}}
{{.Number}} comments under {{.CutOff}} from {{.Start.Format "02 Jan 06 15:04 MST"}} to {{.End.Format "02 Jan 06 15:04 MST"}}.

Top {{.Delta | len}} total negative karma change for this week:
{{range .Delta}}
- **{{.Summary}}** with {{.Count}} posts,
by [/u/{{.Name}}](https://www.reddit.com/user/{{.Name}})
{{- end}}

Top {{.Average | len}} lowest average karma per comment:
{{range .Average}}
- **{{.Summary}}** with {{.Count}} posts,
by [/u/{{.Name}}](https://www.reddit.com/user/{{.Name}})
{{- end}}
{{- end}}

* * *

{{range .Comments -}}
# \#{{.Number}}

Author: [/u/{{.Author}}](https://www.reddit.com/user/{{.Author}}) ({{.Average}} week average)

Date: {{.Created.Format "Monday 02 January 15:04 MST"}}

Score: **{{.Score}}**

Link: [{{.Permalink}}](https://np.reddit.com{{.Permalink}})

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
<div id="title">Report of year {{.Year}} week {{.Week}}</div>

<aside class="md-link"><a href="/reports/source/{{.Year}}/{{.Week}}">source</a></aside>

<article id="report-head">
<h1>Summary</h1>
{{- with .Head}}
	<p>{{.Number}} comments under {{.CutOff}} from {{.Start.Format "02 Jan 06 15:04 MST"}} to {{.End.Format "02 Jan 06 15:04 MST"}}</p>

	<h2>Top {{.Delta | len}} total negative karma change for this week</h2>
	<ol>
	{{- range .Delta}}
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="https://www.reddit.com/user/{{.Name}}">/u/{{.Name}}</a></li>
	{{- end}}
	</ol>

	<h2>Top {{.Average | len}} lowest average karma per comment</h2>
	<ol>
	{{- range .Average}}
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="https://www.reddit.com/user/{{.Name}}">/u/{{.Name}}</a></li>
	{{- end}}
	</ol>
{{- end -}}
</article>

<main>
<h1>Comments</h1>
{{range .Comments}}
<article class="comment">
	<h2>#{{.Number}}</h2>
	<table>
	<tr>
		<td>Author</td>
		<td><a href="https://www.reddit.com/user/{{.Author}}">/u/{{.Author}}</a> ({{.Average}} week average)</td>
	</tr>
	<tr>
		<td>Date</td>
		<td>{{.Created.Format "Monday 02 January 15:04 MST"}}</td>
	</tr>
	<tr>
		<td>Score</td>
		<td>{{.Score}}</td>
	</tr>
	<tr>
		<td>Link</td>
		<td><a href="https://www.reddit.com{{.Permalink}}">{{.Permalink}}</a></td>
	</tr>
	</table>

	<blockquote>
{{.BodyConvert}}
	</blockquote>
</article>
{{end}}
</main>

<footer><a href="#title">back to top</a></footer>

</body>`))

var HTMLCompendiumUserPage = html.Must(html.New("HTMLCompendiumUserPage").Parse(`<!DOCTYPE html>
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Compendium for {{.User.Name}}</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
	<link rel="stylesheet" href="/css/compendium?version={{.Version}}">
</head>
<body>
<div id="title">Compendium for {{.User.Name}}</div>

<nav>
	<ul>
		<li><a href="/compendium/user/{{.User.Name}}#summary">Summary</a></li>
		<li><a href="/compendium/user/{{.User.Name}}#top">Most downvoted</a></li>
		<li><a href="/compendium/user/{{.User.Name}}#subs">Per sub</a></li>
		<li><a href="/compendium/user/{{.User.Name}}#subs-negative">Negative per sub</a></li>
	</ul>
</nav>

<header>
<article>
<h1 id="summary">Summary</h1>
	<table>
		<tr>
			<td>Account created<td>
			<td>{{.User.Created.Format "Monday 02 January 2006 15:04 MST"}}<td>
		</tr>
		<tr>
			<td>Tracked since<td>
			<td>{{.User.Added.Format "Monday 02 January 2006 15:04 MST"}}<td>
		</tr>
		<tr>
			<td>Last scanned<td>
			<td>{{.User.LastScan.Format "Monday 02 January 2006 15:04 MST"}}<td>
		</tr>
		<tr>
			<td>Number of comments<td>
			<td><strong>{{.Summary.Count}}</strong>, with <strong>{{.SummaryNegative.Count}}</strong> negative (<strong>{{.PercentageNegative}}%</strong>)<td>
		</tr>
		<tr>
			<td>Total karma<td>
			<td><strong>{{.Summary.Karma}}</strong>, and <strong>{{.SummaryNegative.Karma}}</strong> if negative only<td>
		</tr>
		<tr>
			<td>Average per comment<td>
			<td><strong>{{.Summary.KarmaPerComment}}</strong>, and <strong>{{.SummaryNegative.KarmaPerComment}}</strong> if negative only<td>
		</tr>
	</table>
</article>
</header>

<main>

<section>
<h1 id="top">Most downvoted</h1>
{{if (.NbTopComments) gt 1}}
<p>First {{.NbTopComments}} comments.</p>
{{end}}
{{range .TopComments}}
<article class="comment">
	<h2># {{.Number}}</h2>
	<table>
	<tr>
		<td>Author</td>
		<td><a href="https://www.reddit.com/user/{{.Author}}">/u/{{.Author}}</a></td>
	</tr>
	<tr>
		<td>Date</td>
		<td>{{.Created.Format "Monday 02 January 15:04 MST"}}</td>
	</tr>
	<tr>
		<td>Score</td>
		<td>{{.Score}}</td>
	</tr>
	<tr>
		<td>Link</td>
		<td><a href="https://www.reddit.com{{.Permalink}}">{{.Permalink}}</a></td>
	</tr>
	</table>

	<blockquote>
{{.BodyConvert}}
	</blockquote>
</article>
{{end}}
<footer><a href="#title">back to top</a></footer>
</section>

<section>
<h1 id="subs">Per sub</h1>
<table class="subs">
<tr>
	<th><strong>Rank</strong></th>
	<th><strong>Sub</strong></th>
	<th><strong>Count</strong></th>
	<th><strong>Karma</strong></th>
	<th><strong>Average</strong></th>
	<th><strong>Last commented</strong></th>
</tr>
{{range .All}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="https://www.reddit.com/r/{{.Sub}}/">{{.Sub}}</a></td>
	<td>{{.Count}}</td>
	<td>{{.Karma}}</td>
	<td>{{.Average}}</td>
	<td>{{.Latest.Format "15:04 2006-01-02 MST"}}</td>
</tr>
{{end}}
</table>
<footer><a href="#title">back to top</a></footer>
</section>

<section>
<h1 id="subs-negative">Negative per sub</h1>
<table class="subs">
<tr>
	<th><strong>Rank</strong></th>
	<th><strong>Sub</strong></th>
	<th><strong>Count</strong></th>
	<th><strong>Karma</strong></th>
	<th><strong>Average</strong></th>
	<th><strong>Last commented</strong></th>
</tr>
{{range .Negative}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="https://www.reddit.com/r/{{.Sub}}/">{{.Sub}}</a></td>
	<td>{{.Count}}</td>
	<td>{{.Karma}}</td>
	<td>{{.Average}}</td>
	<td>{{.Latest.Format "15:04 2006-01-02 MST"}}</td>
</tr>
{{end}}
</table>
<footer><a href="#title">back to top</a></footer>
</section>
</main>
</body>`))

const CSSMain = `:root {
	--main-color: #6a6;
	--sec-color: #5af;
	--bg: #eee;
	--fg: #555;
	--spacing: 0.2em;
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

body {
	color: var(--fg);
	font-size: 0.75em;
	max-width: 63rem;
	margin: 1rem auto;
}

@media (min-width: 40rem) {
	body { font-size: 1em }
}

#title {
	text-align: center;
	font-size: 1.5em;
}

@media (min-width: 40rem) {
	#title { font-size: 2.5em }
}

h1, h2 {
	color: var(--main-color);
	font-size: 1.175em;
}

@media (min-width: 30em) {
	h1 { font-size: 1.5em }
	h2 { font-size: 1.25em }
}

h1 {
	font-weight: bold;
}

h2 {
	font-weight: normal;
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

header {
	margin: var(--spacing);
}

main {
	margin-bottom: 1em;
}`

const CSSReports = `
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
}`

const CSSCompendium = `table.subs {
	border-spacing: calc(2*var(--spacing));
}`
