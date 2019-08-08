package main

import (
	"context"
	"html"
	"math"
	"strings"
	"time"
)

type ReportFactory struct {
	cutOff   int64         // Max acceptable comment score for inclusion in the report
	leeway   time.Duration // Shift of the report's start and end date
	nbTop    uint          // Number of items to summarize the weeks with statistics
	storage  ReportFactoryStorage
	timezone *time.Location // Timezone used to compute weeks, years and corresponding start/end dates
}

func NewReportFactory(storage ReportFactoryStorage, conf ReportConf) ReportFactory {
	return ReportFactory{
		storage:  storage,
		leeway:   conf.Leeway.Value,
		timezone: conf.Timezone.Value,
		cutOff:   conf.CutOff,
		nbTop:    conf.NbTop,
	}
}

func (rf ReportFactory) ReportWeek(ctx context.Context, week_num uint8, year int) (Report, error) {
	start, end := rf.WeekYearToDates(week_num, year)
	report, err := rf.Report(ctx, start.Add(-rf.leeway), end.Add(-rf.leeway))
	if err != nil {
		return report, err
	}
	report.Week = week_num
	report.Year = year
	return report, nil
}

func (rf ReportFactory) Report(ctx context.Context, start, end time.Time) (Report, error) {
	report := Report{}

	comments, err := rf.storage.GetCommentsBelowBetween(ctx, rf.cutOff, start, end)
	if err != nil {
		return report, err
	}

	stats, err := rf.storage.StatsBetween(ctx, rf.cutOff, start, end)
	if err != nil {
		return report, err
	}

	report = Report{
		RawComments:       comments,
		Stats:             stats,
		Start:             start,
		End:               end,
		MaxStatsSummaries: rf.nbTop,
		Timezone:          rf.timezone,
		CutOff:            rf.cutOff,
		Version:           Version,
	}
	return report, nil
}

func (rf ReportFactory) CurrentWeekCoordinates() (uint8, int) {
	year, week := rf.Now().ISOWeek()
	return uint8(week), year
}

func (rf ReportFactory) LastWeekCoordinates() (uint8, int) {
	year, week := rf.Now().AddDate(0, 0, -7).ISOWeek()
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
	in_first_week := time.Date(year, 1, 4, 0, 0, 0, 0, rf.timezone)
	day_position := (in_first_week.Weekday() + 6) % 7
	return in_first_week.AddDate(0, 0, -int(day_position))
}

func (rf ReportFactory) Now() time.Time {
	return time.Now().In(rf.timezone)
}

// Report data structures

type Report struct {
	RawComments       []Comment      // Comments as taken from the database
	Week              uint8          // Week ISO number of the report
	Year              int            // Year of the report
	Start             time.Time      // Start date of the report including any "leeway"
	End               time.Time      // End date of the report including any "leeway"
	Stats             UserStatsMap   // Statistics for all users
	MaxStatsSummaries uint           // Max number of statistics to put in the report's headers to summarize the week
	Timezone          *time.Location // Timezone of dates
	CutOff            int64          // Max score of the comments included in the report
	Version           SemVer         // Version of the software with which the report was made

	CommentBodyConverter func(ReportComment) (interface{}, error) // Optionnal function to convert comments' body to anything
}

func (r Report) Head() ReportHead {
	return ReportHead{
		Number:  len(r.RawComments),
		Average: r.Stats.AveragesToSummaries().Sort().Limit(r.MaxStatsSummaries),
		Delta:   r.Stats.DeltasToSummaries().Sort().Limit(r.MaxStatsSummaries),
		Start:   r.Start,
		End:     r.End,
		CutOff:  r.CutOff,
	}
}

func (r Report) Comments() []ReportComment {
	n := r.Len()
	comments := make([]ReportComment, 0, n)
	for i := 0; i < n; i++ {
		comment := r.Comment(i)
		comment.BodyConverter = r.CommentBodyConverter
		comments = append(comments, comment)
	}
	return comments
}

func (r Report) Comment(i int) ReportComment {
	comment := r.RawComments[i]
	stats := r.Stats[comment.Author]
	return ReportComment{
		Number:    i + 1,
		Average:   int64(math.Round(stats.Average)),
		Author:    comment.Author,
		Created:   comment.Created.In(r.Timezone),
		Score:     comment.Score,
		Sub:       comment.Sub,
		Body:      html.UnescapeString(comment.Body),
		Permalink: comment.Permalink,
	}
}

func (r Report) Len() int {
	return len(r.RawComments)
}

type ReportHead struct {
	Number  int            // Number of comments in the report
	Average StatsSummaries // List of users with the lowest average karma
	Delta   StatsSummaries // List of users with the biggest loss of karma
	Start   time.Time      // Sart date of the report
	End     time.Time      // End date of the report
	CutOff  int64          // Maximum comment score for inclusion in the report
}

type ReportComment struct {
	Number    int       // Position of the comment in the report
	Average   int64     // Average karma for that user
	Author    string    // User name
	Created   time.Time // Date of creation of the comment
	Score     int64     // Score of the comment
	Sub       string    // Subreddit in which the comment was posted
	Permalink string    // Path on reddit to the comment
	Body      string    // Body of the comment as it was typed (in reddit-flavored markdown)

	BodyConverter func(ReportComment) (interface{}, error)
}

func (rc ReportComment) BodyLines() []string {
	return strings.Split(rc.Body, "\n")
}

func (rc ReportComment) BodyConvert() (interface{}, error) {
	if rc.BodyConverter != nil {
		return rc.BodyConverter(rc)
	}
	return rc.Body, nil
}
