package main

import (
	html "html/template"
	text "text/template"
)

// HTMLTemplate is a wrapper for html/template.Template that allows to easily add sub-templates in a chain.
type HTMLTemplate struct {
	html.Template
}

// NewHTMLTemplate creates a new empty template.
func NewHTMLTemplate(name string) *HTMLTemplate {
	return &HTMLTemplate{Template: *html.New(name)}
}

// MustAddParse adds a sub-tree to the template, and panics if there's an error.
func (tmpl *HTMLTemplate) MustAddParse(name, body string) *HTMLTemplate {
	root := &tmpl.Template
	updated := html.Must(root.AddParseTree(name, html.Must(html.New("").Parse(body)).Tree))
	tmpl.Template = *updated
	return tmpl
}

// HTMLTemplates regroups every HTML template so as to easily share common snippets.
var HTMLTemplates = NewHTMLTemplate("Root").MustAddParse("BackToTop",
	`<footer><a href="#title">back to top</a></footer>`,
).MustAddParse("DeltaTable",
	`<table>
<thead>
<tr>
	<th>Rank</th>
	<th>Name</th>
	<th>Karma</th>
	<th>Count</th>
</tr>
</thead>
<tbody>
{{- range .}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="/compendium/user/{{.Name}}">{{.Name}}</a></td>
	<td>{{.Sum}}</td>
	<td>{{.Count}}</td>
</tr>
{{- end}}
</tbody>
</table>`,
).MustAddParse("AverageTable",
	`<table>
<thead>
<tr>
	<th>Rank</th>
	<th>Name</th>
	<th>Average</th>
	<th>Count</th>
</tr>
</thead>
<tbody>
{{- range .}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="/compendium/user/{{.Name}}">{{.Name}}</a></td>
	<td>{{.Average}}</td>
	<td>{{.Count}}</td>
</tr>
{{- end}}
</tbody>
</table>`,
).MustAddParse("Report",
	`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Report of year {{.Year}} week {{.Week}}</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
	<link rel="stylesheet" href="/css/reports?version={{.Version}}">
</head>
<body>
<div id="title"><a href="/reports">Report of year {{.Year}} week {{.Week}}</a></div>

<aside class="md-link"><a href="/reports/source/{{.Year}}/{{.Week}}">source</a></aside>

<nav>
	<ul>
		<li><a href="/reports/{{.Year}}/{{.Week}}#summary">Summary</a></li>
		<li><a href="/reports/{{.Year}}/{{.Week}}#delta">Top negative karma change</a></li>
		<li><a href="/reports/{{.Year}}/{{.Week}}#average">Top average per comment</a></li>
		<li><a href="/reports/{{.Year}}/{{.Week}}#comments">Comments</a></li>
	</ul>
</nav>

<article>
<h1 id="summary">Summary</h1>
{{- with .Header}}
	{{- $dateFormat := "02 Jan 06 15:04 MST"}}
	<p><strong>{{.Len}}</strong> comments under {{.CutOff}} from {{.Start.Format $dateFormat}} to {{.End.Format $dateFormat}}.</p>
	<p>Collective karma change for the week: <strong>{{.Global.Sum}}</strong>.</p>
	<p><a href="/reports/stats/{{.Year}}/{{.Week}}">Complete statistics for the week.</a></p>

	<article>
	<h2 id="delta">Top {{.Delta | len}} total negative karma change for this week</h2>
	{{template "DeltaTable" .Delta}}
	</article>

	<article>
	<h2 id="average">Top {{.Average | len}} lowest average karma per comment</h2>
	{{template "AverageTable" .Average}}
	</article>
{{- end -}}
</article>

<main>
<h1 id="comments">Comments</h1>
{{range .Comments -}}
<article class="comment">
	<h2 id="{{.Number}}"><a href="#{{.Number}}">#{{.Number}}</a></h2>
	<table>
	<tr>
		<td>Author</td>
		<td><a href="/compendium/user/{{.Author}}">{{.Author}}</a> ({{.Stats.Average}} week average)</td>
	</tr>
	<tr>
		<td>Date</td>
		<td>{{.Created.Format "Monday 02 January 15:04"}}</td>
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

{{template "BackToTop"}}

</body>
</html>`,
).MustAddParse("ReportStats",
	`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Statistics of year {{.Year}} week {{.Week}}</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
	<link rel="stylesheet" href="/css/reports?version={{.Version}}">
</head>
<body>
<div id="title"><a href="/reports/{{.Year}}/{{.Week}}">Statistics of year {{.Year}} week {{.Week}}</a></div>

<nav>
	<ul>
		<li><a href="/reports/stats/{{.Year}}/{{.Week}}#summary">Summary</a></li>
		<li><a href="/reports/stats/{{.Year}}/{{.Week}}#delta">Total negative karma change</a></li>
		<li><a href="/reports/stats/{{.Year}}/{{.Week}}#average">Average per comment</a></li>
	</ul>
</nav>

<article>
<h1 id="summary">Summary</h1>
{{- $dateFormat := "02 January 2006 15:04 MST"}}
<p>Statistics from {{.Start.Format $dateFormat}} to {{.End.Format $dateFormat}}.</p>
<p>Number of comments with a negative score: <strong>{{.Global.Count}}</strong>.</p>
<p>Collective karma change: <strong>{{.Global.Sum}}</strong>.</p>
<p>Collective average negative score per comment: <strong>{{.Global.Average}}</strong>.</p>
</article>

<article>
<h1 id="delta">Total negative karma change for this week</h1>
{{template "DeltaTable" .Delta}}
{{template "BackToTop"}}
</article>

<article>
<h1 id="average">Average karma per comment</h1>
{{template "AverageTable" .Average}}
{{template "BackToTop"}}
</article>
</html>`,
).MustAddParse("CompendiumStats",
	`<table>
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
{{range . -}}
<tr>
	<td>{{.Number}}</td>
	<td><a href="/compendium/user/{{.Name}}">{{.Name}}</a></td>
	<td>{{.Stats.Sum}}</td>
	<td>{{.Stats.Count}}</td>
	<td>{{.Stats.Average}}</td>
	<td>
		<span class="detail">{{.Stats.Latest.Format "15:04"}}</span>
		<span>{{.Stats.Latest.Format "2006-01-02"}}</span>
		<span class="detail">{{.Stats.Latest.Format "MST"}}</span>
	</td>
</tr>
{{end -}}
</tbody>
</table>
<p><em>NB: comments from after a sub has been quarantined aren't saved, but comments deleted by the user are kept.</em></p>`,
).MustAddParse("UserComments",
	`{{range . -}}
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
{{end}}`,
).MustAddParse("CompendiumUser",
	`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>{{.User.Name}}</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
	<link rel="stylesheet" href="/css/compendium?version={{.Version}}">
</head>
<body>
<div id="title"><a href="/compendium">Compendium for {{.User.Name}}</a></div>

{{if .Summary.Count -}}
<nav>
	<ul>
		<li><a href="/compendium/user/{{.User.Name}}#summary">Summary</a></li>
		{{if .CommentsLen -}}
		<li><a href="/compendium/user/{{.User.Name}}#top">Most downvoted</a></li>
		{{- end}}
		{{if .Negative -}}
		<li><a href="/compendium/user/{{.User.Name}}#named-negative">Negative per sub</a></li>
		{{- end}}
		{{if .All -}}
		<li><a href="/compendium/user/{{.User.Name}}#named">Per sub</a></li>
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
	{{else if .User.New -}}
		<p><em>Not fully scanned yet.</em></p>
	{{end -}}
	{{$dateFormat := "Monday 02 January 2006 15:04 MST"}}
	<table>
		<tr>
			<td>Link<td>
			<td><a href="https://www.reddit.com/u/{{.User.Name}}">/u/{{.User.Name}}</a><td>
		</tr>
		<tr>
			<td>Account created<td>
			<td>{{.User.Created.Format $dateFormat}}<td>
		</tr>
		{{if .Summary.Count -}}
		<tr>
			<td>Last commented<td>
			<td>{{.Summary.Latest.Format $dateFormat}}<td>
		</tr>
		{{end -}}
		<tr>
			<td>Tracked since<td>
			<td>{{.User.Added.Format $dateFormat}}<td>
		</tr>
		<tr>
			<td>Last scanned<td>
			{{if .User.New -}}
			<td>Not fully scanned yet</td>
			{{else -}}
			<td>{{.User.LastScan.Format $dateFormat}}<td>
			{{end -}}
		</tr>
		{{- if .Summary.Count}}
		<tr>
			<td>Number of comments<td>
			<td><strong>{{.Summary.Count}}</strong>, with <strong>{{.SummaryNegative.Count}}</strong> negative (<strong>{{.PercentageNegative}}%</strong>)<td>
		</tr>
		<tr>
			<td>Total karma<td>
			<td><strong>{{.Summary.Sum}}</strong>, and <strong>{{.SummaryNegative.Sum}}</strong> if negative only<td>
		</tr>
		<tr>
			<td>Average per comment<td>
			<td><strong>{{.Summary.Average}}</strong>, and <strong>{{.SummaryNegative.Average}}</strong> if negative only<td>
		</tr>
		{{- end}}
	</table>
</article>
</header>

<main>

{{if .CommentsLen -}}
<section>
<h1 id="top">Most downvoted</h1>
{{if .CommentsLen -}}
<p>First {{.CommentsLen}} comments.</p>
<p><a href="/compendium/comments/user/{{.User.Name}}">All comments.</a></p>
{{end -}}

{{template "UserComments" .Comments}}

{{template "BackToTop"}}
</section>
{{- end}}

{{if .Negative -}}
<section>
<h1 id="named-negative">Negative per sub</h1>
{{template "CompendiumStats" .Negative}}
{{template "BackToTop"}}
</section>
{{- end}}

{{if .All -}}
<section>
<h1 id="named">Per sub</h1>
{{template "CompendiumStats" .All}}
{{template "BackToTop"}}
</section>
{{- end}}

</main>
</body>
</html>`,
).MustAddParse("CompendiumUserComments",
	`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Comments of {{.User.Name}}</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
</head>
<body>
<div id="title"><a href="/compendium/user/{{.User.Name}}">Comments of {{.User.Name}}</a></div>

{{if .CommentsLen}}
{{if eq (.CommentsLen) (.NbTop) -}}
<nav>
<a href="/compendium/comments/user/{{.User.Name}}?limit={{.CommentsLen}}&offset={{.NextOffset}}">
Next {{.CommentsLen}} comments &rarr;
</a>
</nav>
{{end -}}

{{template "UserComments" .Comments}}

{{template "BackToTop"}}
{{- else}}
<p>No comment yet.</p>
{{end -}}
</html>`,
).MustAddParse("Compendium",
	`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="initial-scale=1"/>
	<title>Compendium</title>
	<link rel="stylesheet" href="/css/main?version={{.Version}}">
	<link rel="stylesheet" href="/css/compendium?version={{.Version}}">
</head>
<body>
<div id="title"><a href="/">Compendium</a></div>

{{- if.Negative}}
<nav>
	<ul>
		<li><a href="/compendium#summary">Summary</a></li>
		<li><a href="/compendium#top">Most downvoted</a></li>
		<li><a href="/compendium#named-negative">Negative karma per user</a></li>
		<li><a href="/compendium#named">Karma per user</a></li>
	</ul>
</nav>

<main>

<article>
<h1 id="summary">Summary</h1>
	<table>
		<tr><td>Registered</td><td>{{.Users | len}}</td></tr>
		<tr><td>Hidden</td><td>{{.HiddenUsersLen}}</td></tr>
		{{- with $user := (index .Users 0)}}
		<tr>
			<td>Last scanned</td>
			<td><a href="/compendium/user/{{$user.Name}}">{{$user.Name}}</a> at {{$user.LastScan.Format "15:04 2006-01-02 MST"}}</td>
		</tr>
		{{- end}}
	</table>
</article>

<section>
<h1 id="top">Most downvoted</h1>
{{if .CommentsLen -}}
<p>Top {{.CommentsLen}} most downvoted comments.</p>
{{end -}}
{{range .Comments -}}
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
{{template "BackToTop"}}
</section>

{{if .Negative -}}
<section>
<h1 id="named-negative">Negative karma per user</h1>
{{template "CompendiumStats" .Negative}}
{{template "BackToTop"}}
</section>
{{- end}}

{{if .All -}}
<section>
<h1 id="named">Karma per user</h1>
<table>
{{template "CompendiumStats" .All}}
{{template "BackToTop"}}
</section>
{{- end}}

</main>
{{- else}}
<p>No comment with negative karma yet.</p>
{{end -}}

</body>
</html>`)

// CSSMain is the main CSS stylesheet, to be served along the result of the HTML templates.
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
	overflow-wrap: break-word;
	word-break: break-all;
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
}

table {
	border-spacing: calc(2*var(--spacing));
}

th {
	font-weight: bold;
}

table tr {
	display: table-row;
}

@media (max-width: 35em) {
	.detail { display: none }
}

@media (max-width: 28em) {
	thead { display: flex }
	table { display: block }
	tbody { display: table }
}

@media (max-width: 25em) {
	table { font-size: 0.9em }
}

@media (max-width: 22.5em) {
	table { font-size: 0.85em }
}

@media (max-width: 21em) {
	table { font-size: 0.8em }
}`

// CSSReports is the CSS stylesheet to be served with the HTML reports.
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

// CSSCompendium is the CSS stylesheet to be served with the HTML compendium pages.
const CSSCompendium = `.suspended {
	color: crimson;
}`

// MarkdownReport is the template for reports in markdow format.
var MarkdownReport = text.Must(text.New("MarkdownReport").Parse(`
{{- with .Header -}}
{{- $dateFormat := "02 Jan 06 15:04 MST"}}
**{{.Len}}** comments under {{.CutOff}} from {{.Start.Format $dateFormat}} to {{.End.Format $dateFormat}}.

Top {{.Delta | len}} total negative karma change for this week:
{{range .Delta}}
- **{{.Sum}}** with {{.Count}} comments,
by [/u/{{.Name}}](https://www.reddit.com/user/{{.Name}})
{{- end}}

Top {{.Average | len}} lowest average karma per comment:
{{range .Average}}
- **{{.Average}}** with {{.Count}} comments,
by [/u/{{.Name}}](https://www.reddit.com/user/{{.Name}})
{{- end}}
{{- end}}

* * *

{{range .Comments -}}
# \#{{.Number}}

Author: [/u/{{.Author}}](https://www.reddit.com/user/{{.Author}}) ({{.Stats.Average}} week average)

Score: **{{.Score}}**

Link: [{{.Permalink}}](https://np.reddit.com{{.Permalink}})

Comment text:

{{range .BodyLines -}}
> {{.}}
{{end}}
{{end}}`))
