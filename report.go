package main

import (
	"html"
	"math"
	"sort"
	"strings"
	"time"
)

type ReportFactory struct {
	storage   ReportFactoryStorage
	Leeway    time.Duration
	Timezone  *time.Location
	Cutoff    int64
	MaxLength uint64
	NbTop     uint
}

func NewReportFactory(storage ReportFactoryStorage, conf ReportConf) ReportFactory {
	return ReportFactory{
		storage:   storage,
		Leeway:    conf.Leeway.Value,
		Timezone:  conf.Timezone.Value,
		Cutoff:    conf.Cutoff,
		MaxLength: conf.MaxLength,
		NbTop:     conf.NbTop,
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
		RawComments:       rf.storage.GetCommentsBelowBetween(rf.Cutoff, start, end),
		Stats:             rf.storage.StatsBetween(start, end),
		Start:             start,
		End:               end,
		MaxStatsSummaries: rf.NbTop,
		Timezone:          rf.Timezone,
	}
}

func (rf ReportFactory) DurationUntilNextWeek() time.Duration {
	year, week := rf.CurrentWeekCoordinates()
	start := rf.WeekNumToStartDate(year, week+1)
	return rf.Now().Sub(start)
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
	in_first_week := time.Date(year, 1, 4, 0, 0, 0, 0, rf.Timezone)
	day_position := (in_first_week.Weekday() + 6) % 7
	return in_first_week.AddDate(0, 0, -int(day_position))
}

func (rf ReportFactory) Now() time.Time {
	return time.Now().In(rf.Timezone)
}

// Report data structures

type Report struct {
	RawComments       []Comment
	Week              uint8
	Year              int
	Start             time.Time
	End               time.Time
	Stats             UserStatsMap
	MaxStatsSummaries uint
	Timezone          *time.Location
}

func (r Report) Head() ReportHead {
	deltas := r.Stats.DeltasToSummaries().Sort()
	if uint(len(deltas)) > r.MaxStatsSummaries {
		deltas = deltas[:r.MaxStatsSummaries]
	}

	averages := r.Stats.AveragesToSummaries().Sort()
	if uint(len(averages)) > r.MaxStatsSummaries {
		averages = averages[:r.MaxStatsSummaries]
	}

	return ReportHead{
		Delta:   deltas,
		Average: averages,
		Start:   r.Start,
		End:     r.End,
	}
}

func (r Report) Comments() []ReportComment {
	n := r.Len()
	comments := make([]ReportComment, 0, n)
	for i := 0; i < n; i++ {
		comments = append(comments, r.Comment(i))
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
		Created:   comment.CreatedTime().In(r.Timezone),
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
	Delta   StatsSummaries
	Average StatsSummaries
	Start   time.Time
	End     time.Time
}

type ReportComment struct {
	Number    int
	Average   int64
	Author    string
	Created   time.Time
	Score     int64
	Sub       string
	Permalink string
	Body      string
}

func (rc ReportComment) BodyLines() []string {
	return strings.Split(rc.Body, "\n")
}

// Statistics data structures

type UserStats struct {
	Name    string
	Average float64
	Delta   int64
	Count   uint64
}

type UserStatsMap map[string]UserStats

func (usc UserStatsMap) DeltasToSummaries() StatsSummaries {
	stats := make([]StatsSummary, 0, len(usc))
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
	stats := make([]StatsSummary, 0, len(usc))
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
