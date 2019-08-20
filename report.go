package main

import (
	"math"
	"time"
)

// ReportFactory generates data structures that define reports about the comments made between two dates,
// and provides method to deal with week numbers, so as to easily generate reports for a specific week.
type ReportFactory struct {
	cutOff   int64         // Max acceptable comment score for inclusion in the report
	leeway   time.Duration // Shift of the report's start and end date
	NbTop    uint          // Number of items to summarize the weeks with statistics
	storage  ReportFactoryStorage
	Timezone *time.Location // Timezone used to compute weeks, years and corresponding start/end dates
}

// NewReportFactory returns a ReportFactory.
func NewReportFactory(storage ReportFactoryStorage, conf ReportConf) ReportFactory {
	return ReportFactory{
		storage:  storage,
		leeway:   conf.Leeway.Value,
		Timezone: conf.Timezone.Value,
		cutOff:   conf.CutOff,
		NbTop:    conf.NbTop,
	}
}

// ReportWeek generates a Report for an ISO week number and a year.
func (rf ReportFactory) ReportWeek(conn *SQLiteConn, weekNum uint8, year int) (Report, error) {
	start, end := rf.WeekYearToDates(weekNum, year)
	report, err := rf.Report(conn, start.Add(-rf.leeway), end.Add(-rf.leeway))
	if err != nil {
		return report, err
	}
	report.Week = weekNum
	report.Year = year
	return report, nil
}

// Report generates a Report between two arbitrary dates.
func (rf ReportFactory) Report(conn *SQLiteConn, start, end time.Time) (Report, error) {
	var comments []Comment
	var stats UserStatsMap

	err := conn.WithTx(func() error {
		var err error
		comments, err = rf.storage.GetCommentsBelowBetween(conn, rf.cutOff, start, end)
		if err != nil {
			return err
		}
		stats, err = rf.storage.StatsBetween(conn, rf.cutOff, start, end)
		return err
	})

	report := Report{
		rawComments:       comments,
		Stats:             stats,
		Start:             start,
		End:               end,
		MaxStatsSummaries: rf.NbTop,
		Timezone:          rf.Timezone,
		CutOff:            rf.cutOff,
		Version:           Version,
	}

	return report, err
}

// CurrentWeekCoordinates returns the week number and year of the current week according to the ReportFactory's time zone.
func (rf ReportFactory) CurrentWeekCoordinates() (uint8, int) {
	year, week := rf.Now().ISOWeek()
	return uint8(week), year
}

// LastWeekCoordinates returns the week number and year of the previous week according to the ReportFactory's time zone.
func (rf ReportFactory) LastWeekCoordinates() (uint8, int) {
	year, week := rf.Now().AddDate(0, 0, -7).ISOWeek()
	return uint8(week), year
}

// WeekYearToDates converts a week number and year to start/end dates according to the ReportFactory's time zone.
func (rf ReportFactory) WeekYearToDates(weekNum uint8, year int) (time.Time, time.Time) {
	weekStart := rf.WeekNumToStartDate(weekNum, year)
	weekEnd := weekStart.AddDate(0, 0, 7)
	return weekStart, weekEnd
}

// WeekNumToStartDate converts a week number and year to the week's start date according to the ReportFactory's time zone.
func (rf ReportFactory) WeekNumToStartDate(weekNum uint8, year int) time.Time {
	return rf.StartOfFirstWeek(year).AddDate(0, 0, int(weekNum-1)*7)
}

// StartOfFirstWeek returns the date at which the first ISO week of the given year starts according to the ReportFactory's time zone.
func (rf ReportFactory) StartOfFirstWeek(year int) time.Time {
	inFirstWeek := time.Date(year, 1, 4, 0, 0, 0, 0, rf.Timezone)
	dayPosition := (inFirstWeek.Weekday() + 6) % 7
	return inFirstWeek.AddDate(0, 0, -int(dayPosition))
}

// Now returns the current date according to the ReportFactory's time zone.
func (rf ReportFactory) Now() time.Time {
	return time.Now().In(rf.Timezone)
}

// Report describes the commenting activity between two dates that may correspond to a week number.
// It is suitable for use in a template.
type Report struct {
	rawComments       []Comment      // Comments as taken from the database
	Week              uint8          // Week ISO number of the report
	Year              int            // Year of the report
	Start             time.Time      // Start date of the report including any "leeway"
	End               time.Time      // End date of the report including any "leeway"
	Stats             UserStatsMap   // Statistics for all users
	MaxStatsSummaries uint           // Max number of statistics to put in the report's headers to summarize the week
	Timezone          *time.Location // Timezone of dates
	CutOff            int64          // Max score of the comments included in the report
	Version           SemVer         // Version of the software with which the report was made

	CommentBodyConverter CommentBodyConverter // Optionnal function to convert comments' body to anything
}

// Head returns a data structure that describes a summary of the Report.
func (r Report) Head() ReportHead {
	return ReportHead{
		Number:  len(r.rawComments),
		Average: r.Stats.AveragesToSummaries().Sort().Limit(r.MaxStatsSummaries),
		Delta:   r.Stats.DeltasToSummaries().Sort().Limit(r.MaxStatsSummaries),
		Start:   r.Start,
		End:     r.End,
		CutOff:  r.CutOff,
	}
}

// Comments returns a slice of data structures describing comments that are suitable for use in templates.
func (r Report) Comments() []ReportComment {
	n := r.Len()
	views := make([]ReportComment, 0, n)
	for i := 0; i < n; i++ {
		comment := r.rawComments[i]
		stats := r.Stats[comment.Author]
		views = append(views, ReportComment{
			CommentView: comment.ToView(uint(i+1), r.Timezone, r.CommentBodyConverter),
			Average:     int64(math.Round(stats.Average)),
		})
	}
	return views
}

// Len returns the number of comments without having to run Comments.
func (r Report) Len() int {
	return len(r.rawComments)
}

// ReportHead describes a summary of a Report suitable for a use in a template.
type ReportHead struct {
	Number  int            // Number of comments in the report
	Average StatsSummaries // List of users with the lowest average karma
	Delta   StatsSummaries // List of users with the biggest loss of karma
	Start   time.Time      // Sart date of the report
	End     time.Time      // End date of the report
	CutOff  int64          // Maximum comment score for inclusion in the report
}

// ReportComment is a specialized version of CommentView for use in Report.
type ReportComment struct {
	CommentView
	Average int64 // Average karma for that user
}
