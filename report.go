package main

import (
	"bytes"
	"html"
	"io"
	"log"
	"math"
	"strings"
	"text/template"
	"time"
)

type ReportTyper struct {
	logger      *log.Logger
	storage     *Storage
	Leeway      time.Duration
	Location    *time.Location
	commentTmpl *template.Template
	headTmpl    *template.Template
	Cutoff      int64
	MaxLength   uint64
	NbTop       int
}

type reportComment struct {
	Number     uint64
	Average    int64
	Author     string
	Score      int64
	Sub        string
	Permalink  string
	QuotedBody string
}

type reportHead struct {
	Delta    []GenStats
	Avg      []GenStats
	Comments []string
}

func NewReportTyper(
	storage *Storage,
	logOut io.Writer,
	timezone string,
	leeway time.Duration,
	cutoff int64,
	maxLength uint64,
	nbTop int,
) (*ReportTyper, error) {
	logger := log.New(logOut, "report: ", log.LstdFlags)

	comment_tmpl := template.Must(template.New("comment").Parse(commentTmpl))
	head_tmpl := template.Must(template.New("report").Parse(reportHeadTmpl))

	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}

	rt := &ReportTyper{
		storage:     storage,
		logger:      logger,
		Leeway:      leeway,
		Location:    location,
		commentTmpl: comment_tmpl,
		headTmpl:    head_tmpl,
		Cutoff:      cutoff,
		MaxLength:   maxLength,
		NbTop:       nbTop,
	}
	return rt, nil
}

func (rt *ReportTyper) ReportLastWeek() ([]string, error) {
	now := time.Now().In(rt.Location)
	year, week := now.AddDate(0, 0, -7).ISOWeek()
	return rt.ReportWeek(uint8(week), year)
}

func (rt *ReportTyper) ReportWeek(week_num uint8, year int) ([]string, error) {
	week_start := WeekNumToStartDate(week_num, year, rt.Location).Add(-rt.Leeway)
	week_end := week_start.AddDate(0, 0, 7)

	batches, err := rt.Report(week_start, week_end)
	if err != nil {
		return nil, err
	}
	return batches, nil
}

func (rt *ReportTyper) Report(start, end time.Time) ([]string, error) {
	comments, err := rt.storage.GetCommentsBelowBetween(rt.Cutoff, start, end)
	if err != nil {
		return nil, err
	}

	stats, err := rt.storage.StatsBetween(start, end)
	if err != nil {
		return nil, err
	}

	typed_comments, err := rt.typeComments(comments, stats)
	if err != nil {
		return nil, err
	}

	head, err := rt.typeReportHead(typed_comments[0], stats)
	if err != nil {
		return nil, err
	}

	batches := make([]string, len(typed_comments))
	batches[0] = head
	for i := 1; i < len(typed_comments); i++ {
		batches[i] = strings.Join(typed_comments[i], "\n")
	}

	return batches, nil
}

func (rt *ReportTyper) typeReportHead(comments []string, stats Stats) (string, error) {
	deltas := stats.DeltasToScores().Sort()
	averages := stats.AveragesToScores().Sort()
	data := reportHead{
		Delta:    deltas[:rt.NbTop],
		Avg:      averages[:rt.NbTop],
		Comments: comments,
	}

	var output bytes.Buffer
	err := rt.headTmpl.Execute(&output, data)
	if err != nil {
		return "", err
	}
	return output.String(), err
}

func (rt *ReportTyper) typeComments(comments []Comment, stats Stats) ([][]string, error) {
	nb_comments := len(comments)
	batches := make([][]string, 0, 10)

	batch := make([]string, 0, nb_comments)
	var total_len uint64 = 0
	for i, comment := range comments {
		average := stats[comment.Author].Average
		formatted, err := rt.CommentToString(uint64(i+1), comment, average)
		if err != nil {
			return nil, err
		}

		len_formatted := uint64(len(formatted))
		total_len += len_formatted
		if total_len > rt.MaxLength {
			batches = append(batches, batch)
			batch = make([]string, 0, nb_comments)
			total_len = len_formatted
		}
		batch = append(batch, formatted)
	}

	batches = append(batches, batch)
	return batches, nil
}

func (rt *ReportTyper) CommentToString(number uint64, comment Comment, average float64) (string, error) {
	body := html.UnescapeString(comment.Body)
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		lines[i] = "> " + lines[i]
	}

	data := reportComment{
		Number:     number,
		Average:    int64(math.Round(average)),
		Author:     comment.Author,
		Score:      comment.Score,
		Sub:        comment.Sub,
		Permalink:  comment.Permalink,
		QuotedBody: strings.Join(lines, "\n"),
	}

	var output bytes.Buffer
	err := rt.commentTmpl.Execute(&output, data)
	if err != nil {
		return "", err
	}
	return output.String(), err
}

func WeekNumToStartDate(week_num uint8, year int, location *time.Location) time.Time {
	start := StartOfFirstWeek(year, location)
	return start.AddDate(0, 0, int(week_num-1)*7)
}

func StartOfFirstWeek(year int, location *time.Location) time.Time {
	in_first_week := time.Date(year, 1, 4, 0, 0, 0, 0, location)
	day_position := (in_first_week.Weekday() + 6) % 7
	return in_first_week.AddDate(0, 0, -int(day_position))
}

// TODO: post link to the rest if the max post length has been reached
const reportHeadTmpl = `Top {{ .Delta | len }} negative **Δk** for this week:
^([**Δk** or "delta k" refers to the total change in karma])
{{ range .Delta }}
 - **{{ .Score }}** with {{ .Count }} posts,
   by [/u/{{ .Author }}](https://reddit.com/user/{{ .Author }})
{{- end }}

Top {{ .Avg | len}} lowest average karma per comment: 
{{ range .Avg }}
 - **{{ .Score }}** with {{ .Count }} posts,
   by [/u/{{ .Author }}](https://reddit.com/user/{{ .Author }})
{{- end }}

* * *

{{ range .Comments }}
{{ . }}
{{ end }}`

const commentTmpl = `
#\#{{ .Number }}

Author: [/u/{{ .Author }}](https://reddit.com/user/{{ .Author }})^(Avg. this week = {{ .Average }} per comment)

Score: **{{ .Score }}**

Subreddit: [/r/{{ .Sub }}](https://reddit.com/r/{{ .Sub }})

Link: [{{ .Permalink }}](https://reddit.com{{ .Permalink }})

Post text:

{{ .QuotedBody }}`
