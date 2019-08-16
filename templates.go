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
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="/compendium/user/{{.Name}}">{{.Name}}</a></li>
	{{- end}}
	</ol>

	<h2>Top {{.Average | len}} lowest average karma per comment</h2>
	<ol>
	{{- range .Average}}
	<li><strong>{{.Summary}}</strong> with {{.Count}} posts, by <a href="/compendium/user/{{.Name}}">{{.Name}}</a></li>
	{{- end}}
	</ol>
{{- end -}}
</article>

<main>
<h1>Comments</h1>
{{range .Comments -}}
<article class="comment">
	<h2 id="{{.Number}}"><a href="#{{.Number}}">#{{.Number}}</a></h2>
	<table>
	<tr>
		<td>Author</td>
		<td><a href="/compendium/user/{{.Author}}">{{.Author}}</a> ({{.Average}} week average)</td>
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
{{.BodyConvert -}}
	</blockquote>
</article>
{{end -}}
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
<div id="title"><a href="/compendium">Compendium for {{.User.Name}}</a></div>

{{if (.Summary.Count) gt 0 -}}
<nav>
	<ul>
		<li><a href="/compendium/user/{{.User.Name}}#summary">Summary</a></li>
		{{if (.TopCommentsLen) gt 0 -}}
		<li><a href="/compendium/user/{{.User.Name}}#top">Most downvoted</a></li>
		{{- end}}
		{{if (.Negative | len) gt 0 -}}
		<li><a href="/compendium/user/{{.User.Name}}#tags-negative">Negative per sub</a></li>
		{{- end}}
		{{if (.All | len) gt 0 -}}
		<li><a href="/compendium/user/{{.User.Name}}#tags">Per sub</a></li>
		{{- end}}
	</ul>
</nav>
{{- end}}

<header>
<article>
<h1 id="summary">Summary</h1>
	{{if .User.Suspended -}}
		<p class="suspended"><strong>Account suspended</strong></p>
	{{else if .User.NotFound -}}
		<p><strong>Account deleted</strong></p>
	{{end -}}
	<table>
		<tr>
			<td>Link<td>
			<td><a href="https://www.reddit.com/u/{{.User.Name}}">/u/{{.User.Name}}</a><td>
		</tr>
		<tr>
			<td>Account created<td>
			<td>{{.User.Created.Format "Monday 02 January 2006 15:04 MST"}}<td>
		</tr>
		{{if (.Summary.Count) gt 0 -}}
		<tr>
			<td>Last commented<td>
			<td>{{.Summary.Latest.Format "Monday 02 January 2006 15:04 MST"}}<td>
		</tr>
		{{end -}}
		<tr>
			<td>Tracked since<td>
			<td>{{.User.Added.Format "Monday 02 January 2006 15:04 MST"}}<td>
		</tr>
		<tr>
			<td>Last scanned<td>
			{{if .User.New -}}
			<td>Not fully scanned yet</td>
			{{else -}}
			<td>{{.User.LastScan.Format "Monday 02 January 2006 15:04 MST"}}<td>
			{{end -}}
		</tr>
		{{- if (.Summary.Count) gt 0}}
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
		{{- end}}
	</table>
</article>
</header>

<main>

{{if (.TopCommentsLen) gt 0 -}}
<section>
<h1 id="top">Most downvoted</h1>
{{if (.TopCommentsLen) gt 1 -}}
<p>First {{.TopCommentsLen}} comments.</p>
{{end -}}
{{range .TopComments -}}
<article class="comment">
	<h2 id="{{.Number}}"><a href="#{{.Number}}">#{{.Number}}</a></h2>
	<table>
	<tr>
		<td>Date</td>
		<td>{{.Created.Format "Monday 02 January 2006 15:04 MST"}}</td>
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
{{end -}}
<footer><a href="#title">back to top</a></footer>
</section>
{{- end}}

{{if (.Negative | len) gt 0 -}}
<section>
<h1 id="tags-negative">Negative per sub</h1>
<table class="tags">
<thead>
<tr>
	<th><strong>Rank</strong></th>
	<th><strong>Sub</strong></th>
	<th><strong>Karma</strong></th>
	<th><strong>Count</strong></th>
	<th><strong>Average</strong></th>
	<th><strong>Last commented</strong></th>
</tr>
</thead>
<tbody>
{{range .Negative -}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="https://www.reddit.com/r/{{.Tag}}/">{{.Tag}}</a></td>
	<td>{{.Karma}}</td>
	<td>{{.Count}}</td>
	<td>{{.Average}}</td>
	<td>
		<span class="detail">{{.Latest.Format "15:04"}}</span>
		<span>{{.Latest.Format "2006-01-02"}}</span>
		<span class="detail">{{.Latest.Format "MST"}}</span>
	</td>
</tr>
{{end -}}
</tbody>
</table>
<p><em>NB: comments from after a sub has been quarantined aren't saved, but comments deleted by the user are kept.</em></p>
<footer><a href="#title">back to top</a></footer>
</section>
{{- end}}

{{if (.All | len) gt 0 -}}
<section>
<h1 id="tags">Per sub</h1>
<table class="tags">
<thead>
<tr>
	<th>Rank</th>
	<th>Sub</th>
	<th>Karma</th>
	<th>Count</th>
	<th>Average</th>
	<th>Last commented</th>
</tr>
</thead>
<tbody>
{{range .All -}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="https://www.reddit.com/r/{{.Tag}}/">{{.Tag}}</a></td>
	<td>{{.Karma}}</td>
	<td>{{.Count}}</td>
	<td>{{.Average}}</td>
	<td>
		<span class="detail">{{.Latest.Format "15:04"}}</span>
		<span>{{.Latest.Format "2006-01-02"}}</span>
		<span class="detail">{{.Latest.Format "MST"}}</span>
	</td>
</tr>
{{end -}}
</tbody>
</table>
<p><em>NB: comments from after a sub has been quarantined aren't saved, but comments deleted by the user are kept.</em></p>
<footer><a href="#title">back to top</a></footer>
</section>
{{- end}}

</main>
</body>`))

var HTMLCompendium = html.Must(html.New("HTMLCompendium").Parse(`<!DOCTYPE html>
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Compendium</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
	<link rel="stylesheet" href="/css/compendium?version={{.Version}}">
</head>
<body>
<div id="title">Compendium</div>

{{- if (.Negative | len) gt 0}}
<nav>
	<ul>
		<li><a href="/compendium#top">Most downvoted</a></li>
		<li><a href="/compendium#tags-negative">Negative karma per user</a></li>
		<li><a href="/compendium#tags">Karma per user</a></li>
	</ul>
</nav>

<main>

<p>{{.Users | len}} registered users, of which {{.HiddenUsersLen}} are hidden.</p>

<section>
<h1 id="top">Most downvoted</h1>
{{if (.TopCommentsLen) gt 1 -}}
<p>Top {{.TopCommentsLen}} most downvoted comments.</p>
{{end -}}
{{range .TopComments -}}
<article class="comment">
	<h2 id="{{.Number}}"><a href="#{{.Number}}">#{{.Number}}</a></h2>
	<table>
	<tr>
		<td>Author</td>
		<td><a href="/compendium/user/{{.Author}}">{{.Author}}</a></td>
	</tr>
	<tr>
		<td>Date</td>
		<td>{{.Created.Format "Monday 02 January 2006 15:04 MST"}}</td>
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
{{end -}}
<footer><a href="#title">back to top</a></footer>
</section>

{{if (.Negative | len) gt 0 -}}
<section>
<h1 id="tags-negative">Negative karma per user</h1>
<table class="tags">
<thead>
<tr>
	<th><strong>Rank</strong></th>
	<th><strong>Name</strong></th>
	<th><strong>Karma</strong></th>
	<th><strong>Count</strong></th>
	<th><strong>Average</strong></th>
	<th><strong>Last commented</strong></th>
</tr>
</thead>
<tbody>
{{range .Negative -}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="/compendium/user/{{.Tag}}">{{.Tag}}</a></td>
	<td>{{.Karma}}</td>
	<td>{{.Count}}</td>
	<td>{{.Average}}</td>
	<td>
		<span class="detail">{{.Latest.Format "15:04"}}</span>
		<span>{{.Latest.Format "2006-01-02"}}</span>
		<span class="detail">{{.Latest.Format "MST"}}</span>
	</td>
</tr>
{{end -}}
</tbody>
</table>
<footer><a href="#title">back to top</a></footer>
</section>
{{- end}}

{{if (.All | len) gt 0 -}}
<section>
<h1 id="tags">Karma per user</h1>
<table class="tags">
<thead>
<tr>
	<th>Rank</th>
	<th>Name</th>
	<th>Karma</th>
	<th>Count</th>
	<th>Average</th>
	<th>Last commented</th>
</tr>
</thead>
<tbody>
{{range .All -}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="/compendium/user/{{.Tag}}">{{.Tag}}</a></td>
	<td>{{.Karma}}</td>
	<td>{{.Count}}</td>
	<td>{{.Average}}</td>
	<td>
		<span class="detail">{{.Latest.Format "15:04"}}</span>
		<span>{{.Latest.Format "2006-01-02"}}</span>
		<span class="detail">{{.Latest.Format "MST"}}</span>
	</td>
</tr>
{{end -}}
</tbody>
</table>
<footer><a href="#title">back to top</a></footer>
</section>
{{- end}}

</main>
{{- else}}
<p>No comment with negative karma yet.</p>
{{end -}}

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
	max-width: 62.5em;
	margin: 1em auto;
	font-size: 0.75em;
}

#title {
	text-align: center;
	font-size: 2em;
}

h1, h2 {
	color: var(--main-color);
}

h1 {
	font-weight: bold;
	font-size: 1.5em;
}

h2 {
	font-weight: normal;
	font-size: 1.25em;
}

#title a, h1 a, h2 a, #title a:visited, h1 a:visited, h2 a:visited {
	color: inherit;
	text-decoration: none;
}

#title a:hover, h1 a:hover, h2 a:hover {
	color: var(--sec-color);
	text-decoration: underline;
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
}

@media (min-width: 50em) {
	body { font-size: 1em }
}`

const CSSReports = `
aside.md-link {
	font-weight: bold;
	margin-right: 1em;
	float: right;
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

const CSSCompendium = `table.tags {
	border-spacing: calc(2*var(--spacing));
}

table.tags th {
	font-weight: bold;
}

table.tags tr {
	display: table-row;
}

@media (max-width: 35em) {
	.detail { display: none }
}

@media (max-width: 28em) {
	table.tags thead { display: flex }
	table.tags { display: block }
	table.tags tbody { display: table }
}

@media (max-width: 25em) {
	table.tags { font-size: 0.9em }
}

@media (max-width: 22.5em) {
	table.tags { font-size: 0.85em }
}

@media (max-width: 21em) {
	table.tags { font-size: 0.8em }
}

.suspended {
	color: crimson;
}`
