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

const comment_template = `#\#{{.Number}}

Author: [/u/{{.Author}}](https://reddit.com/user/{{.Author}})^(Avg. this week = {{.Average}} per comment)

Score: **{{.Score}}**

Subreddit: [/r/{{.Sub}}](https://reddit.com/r/{{.Sub}})

Link: [{{.Permalink}}](https://reddit.com{{.Permalink}})

Post text:

{{.QuotedBody}}`

// TODO: post link to the rest if the max post length has been reached
const report_head_template = `Greatest negative **Δk** for this week:
**{{.Delta.Score}}** with {{.Delta.Count}} posts,
by [/u/{{.Delta.Author}}](https://reddit.com/user/{{.Delta.Author}})

^([**Δk** or "delta k" refers to the total change in karma])

Lowest average karma per comment: **{{.Avg.Score}}** with {{.Avg.Count}} posts,
by [/u/{{.Avg.Author}}](https://reddit.com/user/{{.Avg.Author}})

* * *

{{range .Comments}}
{{ . }}
{{ end }}`

type ReportTyper struct {
	logger         *log.Logger
	storage        *Storage
	Leeway         time.Duration
	Location       *time.Location
	commentTmpl    *template.Template
	reportHeadTmpl *template.Template
	Cutoff         int64
	MaxLength      uint64
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

type reportStats struct {
	Author string
	Score  int64
	Count  uint64
}

type reportHead struct {
	Delta    reportStats
	Avg      reportStats
	Comments []string
}

func NewReportTyper(
	storage *Storage,
	logOut io.Writer,
	timezone string,
	leeway time.Duration,
	cutoff int64,
	maxLength uint64,
) (*ReportTyper, error) {
	logger := log.New(logOut, "report: ", log.LstdFlags)
	comment_tmpl := template.Must(template.New("comment").Parse(comment_template))
	report_head_tmpl := template.Must(template.New("report").Parse(report_head_template))
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}

	rt := &ReportTyper{
		storage:        storage,
		logger:         logger,
		Leeway:         leeway,
		Location:       location,
		commentTmpl:    comment_tmpl,
		reportHeadTmpl: report_head_tmpl,
		Cutoff:         cutoff,
		MaxLength:      maxLength,
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

	averages, err := rt.storage.Averages(start, end)
	if err != nil {
		return nil, err
	}

	name, avg_score, count, err := rt.storage.LowestAverageBetween(start, end)
	avg := reportStats{
		Author: name,
		Score:  round(avg_score),
		Count:  count,
	}
	if err != nil {
		return nil, err
	}

	name, delta_score, count, err := rt.storage.LowestDeltaBetween(start, end)
	if err != nil {
		return nil, err
	}
	delta := reportStats{
		Author: name,
		Score:  delta_score,
		Count:  count,
	}

	typed_comments, err := rt.typeComments(comments, averages)
	if err != nil {
		return nil, err
	}

	head, err := rt.typeReportHead(typed_comments[0], avg, delta)
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

func (rt *ReportTyper) typeReportHead(comments []string, average reportStats, delta reportStats) (string, error) {
	data := reportHead{
		Delta:    delta,
		Avg:      average,
		Comments: comments,
	}
	var output bytes.Buffer
	err := rt.reportHeadTmpl.Execute(&output, data)
	if err != nil {
		return "", err
	}
	return output.String(), err
}

func (rt *ReportTyper) typeComments(comments []Comment, averages map[string]float64) ([][]string, error) {
	nb_comments := len(comments)
	batches := make([][]string, 0, 10)

	batch := make([]string, 0, nb_comments)
	var total_len uint64 = 0
	for i, comment := range comments {
		average := averages[comment.Author]
		formatted, err := rt.CommentToString(uint64(i+1), comment, average)
		if err != nil {
			return nil, err
		}

		total_len += uint64(len(formatted))
		if total_len > rt.MaxLength {
			batches = append(batches, batch)
			batch = make([]string, 0, nb_comments)
			total_len = 0
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
		Average:    round(average),
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

// TODO: wait until Go 1.10 to (finally...) have a built-in rounding function
func round(val float64) int64 {
	var rounded int64
	if val < 0 {
		rounded = int64(math.Ceil(val - 0.5))
	} else {
		rounded = int64(math.Floor(val + 0.5))
	}
	return rounded
}
