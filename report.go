package main

import (
	"bytes"
	"html"
	"math"
	"sort"
	"strings"
	"text/template"
	"time"
)

type ReportFactory struct {
	storage         ReportFactoryStorage
	Leeway          time.Duration
	Timezone        *time.Location
	Cutoff          int64
	MaxLength       uint64
	NbTop           int
	HeadTemplate    *template.Template
	CommentTemplate *template.Template
}

func NewReportFactory(storage ReportFactoryStorage, conf ReportConf) ReportFactory {
	return ReportFactory{
		storage:         storage,
		Leeway:          conf.Leeway.Value,
		Timezone:        conf.Timezone.Value,
		Cutoff:          conf.Cutoff,
		MaxLength:       conf.MaxLength,
		NbTop:           conf.NbTop,
		HeadTemplate:    template.Must(template.New("Head").Parse(tReportHead)),
		CommentTemplate: template.Must(template.New("Comment").Parse(tReportComment)),
	}
}

func (rf ReportFactory) ReportWeek(week_num uint8, year int) Report {
	start, end := rf.WeekYearToDates(week_num, year)
	report := rf.Report(start.Add(-rf.Leeway), end.Add(-rf.Leeway))
	report.Week = week_num
	report.Year = year
	return report
}

func (rf ReportFactory) Report(start, end time.Time) Report {
	return Report{
		Comments:          rf.storage.GetCommentsBelowBetween(rf.Cutoff, start, end),
		Stats:             rf.storage.StatsBetween(start, end),
		Start:             start,
		End:               end,
		DateFormat:        time.RFC822,
		MaxStatsSummaries: rf.NbTop,
		HeadTemplate:      rf.HeadTemplate,
		CommentTemplate:   rf.CommentTemplate,
	}
}

func (rf ReportFactory) CurrentWeekCoordinates() (uint8, int) {
	year, week := time.Now().In(rf.Timezone).ISOWeek()
	return uint8(week), year
}

func (rf ReportFactory) LastWeekCoordinates() (uint8, int) {
	year, week := time.Now().In(rf.Timezone).AddDate(0, 0, -7).ISOWeek()
	return uint8(week), year
}

func (rf ReportFactory) WeekYearToDates(week_num uint8, year int) (time.Time, time.Time) {
	week_start := rf.WeekNumToStartDate(week_num, year)
	week_end := week_start.AddDate(0, 0, 7)
	return week_start, week_end
}

func (rf ReportFactory) WeekNumToStartDate(week_num uint8, year int) time.Time {
	return rf.StartOfFirstWeek(year).AddDate(0, 0, int(week_num-1)*7)
}

func (rf ReportFactory) StartOfFirstWeek(year int) time.Time {
	in_first_week := time.Date(year, 1, 4, 0, 0, 0, 0, rf.Timezone)
	day_position := (in_first_week.Weekday() + 6) % 7
	return in_first_week.AddDate(0, 0, -int(day_position))
}

type Report struct {
	Comments []Comment
	// Time
	Week       uint8
	Year       int
	Start      time.Time
	End        time.Time
	DateFormat string
	// Stats
	Stats             UserStatsMap
	MaxStatsSummaries int
	// Formating
	HeadTemplate    *template.Template
	CommentTemplate *template.Template
}

func (r Report) FormatHead() string {
	deltas := r.Stats.DeltasToSummaries().Sort()
	if len(deltas) > r.MaxStatsSummaries {
		deltas = deltas[:r.MaxStatsSummaries]
	}

	averages := r.Stats.AveragesToSummaries().Sort()
	if len(averages) > r.MaxStatsSummaries {
		averages = averages[:r.MaxStatsSummaries]
	}

	data := map[string]interface{}{
		"Delta":   deltas,
		"Average": averages,
		"Start":   r.Start.Format(r.DateFormat),
		"End":     r.End.Format(r.DateFormat),
	}

	var output bytes.Buffer
	autopanic(r.HeadTemplate.Execute(&output, data))
	return output.String()
}

func (r Report) Len() int {
	return len(r.Comments)
}

func (r Report) Comment(i int) ReportComment {
	comment := r.Comments[i]
	return NewReportComment(i+1, comment, r.Stats[comment.Author], r.CommentTemplate)
}

func (r Report) String() string {
	formatted := []string{r.FormatHead()}

	nb_comments := r.Len()
	for i := 0; i < nb_comments; i++ {
		formatted = append(formatted, r.Comment(i).String())
	}

	return strings.Join(formatted, "\n\n\n")
}

type ReportComment struct {
	Number     int
	Average    int64
	Author     string
	Score      int64
	Sub        string
	Permalink  string
	Body       string
	QuotedBody string
	Template   *template.Template
}

func NewReportComment(number int, comment Comment, stats UserStats, tmpl *template.Template) ReportComment {
	body := html.UnescapeString(comment.Body)
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		lines[i] = "> " + lines[i]
	}

	return ReportComment{
		Number:     number,
		Average:    int64(math.Round(stats.Average)),
		Author:     comment.Author,
		Score:      comment.Score,
		Sub:        comment.Sub,
		Body:       comment.Body,
		Permalink:  comment.Permalink,
		QuotedBody: strings.Join(lines, "\n"),
		Template:   tmpl,
	}
}

func (rc ReportComment) String() string {
	var output bytes.Buffer
	autopanic(rc.Template.Execute(&output, rc))
	return output.String()
}

// Statistics

type UserStats struct {
	Name    string
	Average float64
	Delta   int64
	Count   uint64
}

type UserStatsMap map[string]UserStats

func (usc UserStatsMap) DeltasToSummaries() StatsSummaries {
	var stats []StatsSummary
	for name, data := range usc {
		stats = append(stats, StatsSummary{
			Name:    name,
			Count:   data.Count,
			Summary: data.Delta,
		})
	}
	return stats
}

func (usc UserStatsMap) AveragesToSummaries() StatsSummaries {
	var stats []StatsSummary
	for name, data := range usc {
		stats = append(stats, StatsSummary{
			Name:    name,
			Count:   data.Count,
			Summary: int64(math.Round(data.Average)),
		})
	}
	return stats
}

type StatsSummary struct {
	Name    string
	Count   uint64
	Summary int64
}

type StatsSummaries []StatsSummary

func (s StatsSummaries) Len() int {
	return len(s)
}

func (s StatsSummaries) Less(i, j int) bool {
	return s[i].Summary < s[j].Summary
}

func (s StatsSummaries) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s StatsSummaries) Sort() StatsSummaries {
	sort.Sort(s)
	return s
}

const tReportHead = `From {{ .Start }} to {{ .End }}.

Top {{ .Delta | len }} negative **Δk** for this week:
^([**Δk** or "delta k" refers to the total change in karma])
{{ range .Delta }}
 - **{{ .Summary }}** with {{ .Count }} posts,
   by [/u/{{ .Name }}](https://reddit.com/user/{{ .Name }})
{{- end }}

Top {{ .Average | len}} lowest average karma per comment:
{{ range .Average }}
 - **{{ .Summary }}** with {{ .Count }} posts,
   by [/u/{{ .Name }}](https://reddit.com/user/{{ .Name }})
{{- end }}

* * *`

const tReportComment = `# \#{{ .Number }}

Author: [/u/{{ .Author }}](https://reddit.com/user/{{ .Author }})^(Avg. this week = {{ .Average }} per comment)

Score: **{{ .Score }}**

Subreddit: [/r/{{ .Sub }}](https://reddit.com/r/{{ .Sub }})

Link: [{{ .Permalink }}](https://reddit.com{{ .Permalink }})

Post text:

{{ .QuotedBody }}`
