package main

import (
	"bytes"
	"fmt"
	"html"
	"math"
	"strings"
	"text/template"
	"time"
)

type ReportConf struct {
	Leeway    Duration `json:"leeway"`
	Timezone  Timezone `json:"timezone"`
	Cutoff    int64    `json:"cutoff"`
	MaxLength uint64   `json:"max_length"`
	NbTop     int      `json:"nb_top"`
}

type ReportTyper struct {
	Conf        ReportConf
	storage     ReportTyperStorage
	commentTmpl *template.Template
	headTmpl    *template.Template
}

type ReportTyperStorage interface {
	GetCommentsBelowBetween(int64, time.Time, time.Time) ([]Comment, error)
	StatsBetween(time.Time, time.Time) (Stats, error)
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
	Start    string
	End      string
}

func NewReportTyper(storage ReportTyperStorage, conf ReportConf) *ReportTyper {
	comment_tmpl := template.Must(template.New("comment").Parse(commentTmpl))
	head_tmpl := template.Must(template.New("report").Parse(reportHeadTmpl))

	rt := &ReportTyper{
		Conf:        conf,
		storage:     storage,
		commentTmpl: comment_tmpl,
		headTmpl:    head_tmpl,
	}
	return rt
}

func (rt *ReportTyper) ReportLastWeek() ([]string, error) {
	return rt.ReportWeek(LastWeekCoordinates(rt.Conf.Timezone.Value))
}

func (rt *ReportTyper) ReportWeek(week_num uint8, year int) ([]string, error) {
	start, end := WeekYearToDates(week_num, year, rt.Conf.Timezone.Value)
	return rt.Report(start.Add(-rt.Conf.Leeway.Value), end.Add(-rt.Conf.Leeway.Value))
}

func (rt *ReportTyper) Report(start, end time.Time) ([]string, error) {
	comments, err := rt.storage.GetCommentsBelowBetween(rt.Conf.Cutoff, start, end)
	if err != nil {
		return nil, err
	}

	if len(comments) == 0 {
		return nil, fmt.Errorf("no comment found between %s and %s", start, end)
	}

	stats, err := rt.storage.StatsBetween(start, end)
	if err != nil {
		return nil, err
	}

	typed_comments := rt.typeComments(comments, stats)
	head := rt.typeReportHead(typed_comments[0], stats, start, end)

	batches := make([]string, len(typed_comments))
	batches[0] = head
	for i := 1; i < len(typed_comments); i++ {
		batches[i] = strings.Join(typed_comments[i], "\n")
	}

	return batches, nil
}

func (rt *ReportTyper) typeReportHead(comments []string, stats Stats, start, end time.Time) string {
	deltas := stats.DeltasToScores().Sort()
	if len(deltas) > rt.Conf.NbTop {
		deltas = deltas[:rt.Conf.NbTop]
	}

	averages := stats.AveragesToScores().Sort()
	if len(averages) > rt.Conf.NbTop {
		averages = averages[:rt.Conf.NbTop]
	}

	data := reportHead{
		Delta:    deltas,
		Avg:      averages,
		Comments: comments,
		Start:    start.Format(time.RFC822),
		End:      end.Format(time.RFC822),
	}

	var output bytes.Buffer
	if err := rt.headTmpl.Execute(&output, data); err != nil {
		panic(err)
	}
	return output.String()
}

func (rt *ReportTyper) typeComments(comments []Comment, stats Stats) [][]string {
	nb_comments := len(comments)
	batches := make([][]string, 0, 10)

	batch := make([]string, 0, nb_comments)
	var total_len uint64 = 0
	for i, comment := range comments {
		average := stats[comment.Author].Average
		formatted := rt.CommentToString(uint64(i+1), comment, average)

		len_formatted := uint64(len(formatted))
		total_len += len_formatted
		if total_len > rt.Conf.MaxLength {
			batches = append(batches, batch)
			batch = make([]string, 0, nb_comments)
			total_len = len_formatted
		}
		batch = append(batch, formatted)
	}

	batches = append(batches, batch)
	return batches
}

func (rt *ReportTyper) CommentToString(number uint64, comment Comment, average float64) string {
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
	if err := rt.commentTmpl.Execute(&output, data); err != nil {
		panic(err)
	}
	return output.String()
}

func LastWeekCoordinates(location *time.Location) (uint8, int) {
	now := time.Now().In(location)
	year, week := now.AddDate(0, 0, -7).ISOWeek()
	return uint8(week), year
}

func WeekYearToDates(week_num uint8, year int, location *time.Location) (time.Time, time.Time) {
	week_start := WeekNumToStartDate(week_num, year, location)
	week_end := week_start.AddDate(0, 0, 7)
	return week_start, week_end
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
const reportHeadTmpl = `From {{ .Start }} to {{ .End }}.

Top {{ .Delta | len }} negative **Δk** for this week:
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
# \#{{ .Number }}

Author: [/u/{{ .Author }}](https://reddit.com/user/{{ .Author }})^(Avg. this week = {{ .Average }} per comment)

Score: **{{ .Score }}**

Subreddit: [/r/{{ .Sub }}](https://reddit.com/r/{{ .Sub }})

Link: [{{ .Permalink }}](https://reddit.com{{ .Permalink }})

Post text:

{{ .QuotedBody }}

`
