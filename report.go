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

// 2. turn each comment into a single string
// 3. compute the total length of the post and cut as necessary
// (bot) 4. post the thing

type ReportTyper struct {
	logger      *log.Logger
	storage     *Storage
	Leeway      time.Duration
	Location    *time.Location
	commentTmpl *template.Template
	Cutoff      int64
	MaxLength   uint64
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

func NewReportTyper(
	storage *Storage,
	logOut io.Writer,
	timezone string,
	leeway time.Duration,
	cutoff int64,
	maxLength uint64,
) (*ReportTyper, error) {
	logger := log.New(logOut, "report: ", log.LstdFlags)
	tmpl := template.Must(template.New("comment").Parse(comment_template))
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}

	rt := &ReportTyper{
		storage:     storage,
		logger:      logger,
		Leeway:      leeway,
		Location:    location,
		commentTmpl: tmpl,
		Cutoff:      cutoff,
		MaxLength:   maxLength,
	}
	return rt, nil
}

func (rt *ReportTyper) ReportLastWeek() ([]string, error) {
	now := time.Now().In(rt.Location)
	year, week := now.ISOWeek()
	return rt.ReportWeek(uint8(week), year)
}

func (rt *ReportTyper) ReportWeek(week_num uint8, year int) ([]string, error) {
	week_start := WeekNumToStartDate(week_num, year, rt.Location).Add(-rt.Leeway)
	week_end := week_start.AddDate(0, 0, week_start.Day()+7)

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

	nb_comments := len(comments)
	batches := make([]string, nb_comments)

	batch := make([]string, nb_comments)
	var total_len uint64 = 0
	for i, comment := range comments {
		average := averages[comment.Author]
		formatted, err := rt.CommentToString(uint64(i), comment, average)
		if err != nil {
			return nil, err
		}

		total_len += uint64(len(formatted))
		if total_len > rt.MaxLength {
			batches = append(batches, strings.Join(batch, "\n"))
			batch = make([]string, nb_comments)
			total_len = 0
		}
		batch = append(batch, formatted)
	}

	batches = append(batches, strings.Join(batch, "\n"))
	return batches, nil
}

func (rt *ReportTyper) CommentToString(number uint64, comment Comment, average float64) (string, error) {
	body := html.UnescapeString(comment.Body)
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		lines[i] = "> " + lines[i]
	}

	var rounded_avg int64
	// TODO: wait until Go 1.10 to (finally...) have a built-in rounding function
	if average < 0 {
		rounded_avg = int64(math.Ceil(average - 0.5))
	} else {
		rounded_avg = int64(math.Floor(average + 0.5))
	}

	data := reportComment{
		Number:     number,
		Average:    rounded_avg,
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
	val := start.AddDate(0, 0, int(week_num-1)*7)
	return val
}

func StartOfFirstWeek(year int, location *time.Location) time.Time {
	in_first_week := time.Date(year, 1, 4, 0, 0, 0, 0, location)
	day_position := (in_first_week.Weekday() + 6) % 7
	return in_first_week.AddDate(0, 0, -int(day_position))
}
