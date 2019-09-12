package main

import (
	"time"
)

// ReportFactory generates data structures that define reports about the comments made between two dates,
// and provides method to deal with week numbers, so as to easily generate reports for a specific week.
type ReportFactory struct {
	cutOff   int64          // Max acceptable comment score for inclusion in the report
	leeway   time.Duration  // Shift of the report's start and end date
	nbTop    uint           // Number of items to summarize the weeks with statistics
	Timezone *time.Location // Timezone used to compute weeks, years and corresponding start/end dates
}

// NewReportFactory returns a ReportFactory.
func NewReportFactory(conf ReportConf) ReportFactory {
	return ReportFactory{
		leeway:   conf.Leeway.Value,
		Timezone: conf.Timezone.Value,
		cutOff:   conf.CutOff,
		nbTop:    conf.NbTop,
	}
}

// ReportWeek generates a Report for an ISO week number and a year.
func (rf ReportFactory) ReportWeek(conn StorageConn, weekNum uint8, year int) (Report, error) {
	start, end := rf.WeekYearToDates(weekNum, year)
	report, err := rf.Report(conn, start.Add(-rf.leeway), end.Add(-rf.leeway))
	if err != nil {
		return report, err
	}
	report.Week = weekNum
	report.Year = year
	return report, nil
}

// StatsWeek generates a statistical summary of the activity for an ISO week number and year.
func (rf ReportFactory) StatsWeek(conn StorageConn, weekNum uint8, year int) (ReportHeader, error) {
	start, end := rf.WeekYearToDates(weekNum, year)
	header, err := rf.Stats(conn, start.Add(-rf.leeway), end.Add(-rf.leeway))
	if err != nil {
		return header, err
	}
	header.Week = weekNum
	header.Year = year
	return header, nil
}

// Report generates a Report between two arbitrary dates.
func (rf ReportFactory) Report(conn StorageConn, start, end time.Time) (Report, error) {
	var comments []Comment
	var stats StatsCollection

	err := conn.WithTx(func() error {
		var err error
		comments, err = conn.GetCommentsBelowBetween(rf.cutOff, start, end)
		if err != nil {
			return err
		}
		stats, err = conn.StatsBetween(start, end)
		return err
	})

	report := Report{
		ReportInfo: ReportInfo{
			CutOff:   rf.cutOff,
			End:      end,
			Start:    start,
			Timezone: rf.Timezone,
			Version:  Version,
		},
		comments: comments,
		nbTop:    rf.nbTop,
		stats:    stats.Filter(func(s Stats) bool { return s.Sum < rf.cutOff }),
	}

	return report, err
}

// Stats generates a statistical summary of the activity between two arbitrary dates.
func (rf ReportFactory) Stats(conn StorageConn, start, end time.Time) (ReportHeader, error) {
	stats, err := conn.StatsBetween(start, end)
	global := stats.Stats()
	report := ReportHeader{
		ReportInfo: ReportInfo{
			CutOff:   rf.cutOff,
			End:      end,
			Start:    start,
			Timezone: rf.Timezone,
			Version:  Version,
		},
		Average: stats.OrderBy(func(a, b Stats) bool { return a.Average < b.Average }).ToView(rf.Timezone),
		Delta:   stats.ToView(rf.Timezone),
		Global:  global.ToView(0, rf.Timezone),
		Len:     global.Count,
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

// ReportInfo describes the metadata of a report.
type ReportInfo struct {
	CutOff   int64          // Max score of the comments included in the report
	End      time.Time      // End date of the report
	Start    time.Time      // Start date of the report
	Timezone *time.Location // Timezone of dates
	Version  SemVer         // Version of the software with which the report was made
	Week     uint8          // ISO Week number of the report
	Year     int            // Year of the report
}

// ReportHeader describes a summary of a Report suitable for a use in a template.
type ReportHeader struct {
	ReportInfo
	Global  StatsView
	Average []StatsView // List of users with the lowest average karma
	Delta   []StatsView // List of users with the biggest loss of karma
	Len     uint64      // Number of comments in the report
}

// ReportComment is a specialized version of CommentView for use in Report.
type ReportComment struct {
	CommentView
	Stats StatsView // Stats for that user
}

// Report describes the commenting activity between two dates that may correspond to a week number.
// It is suitable for use in a template.
type Report struct {
	ReportInfo
	nbTop    uint            // Max number of statistics to put in the report's headers to summarize the week
	stats    StatsCollection // Statistics for all users
	comments []Comment

	CommentBodyConverter CommentBodyConverter
}

// Header returns a data structure that describes a summary of the Report.
func (r Report) Header() ReportHeader {
	return ReportHeader{
		ReportInfo: r.ReportInfo,
		Average:    r.stats.OrderBy(func(a, b Stats) bool { return a.Average < b.Average }).Limit(r.nbTop).ToView(r.Timezone),
		Delta:      r.stats.Limit(r.nbTop).ToView(r.Timezone),
		Global:     r.stats.Stats().ToView(0, r.Timezone),
		Len:        r.Len(),
	}
}

// Comments returns a slice of data structures describing comments that are suitable for use in templates.
func (r Report) Comments() []ReportComment {
	n := r.Len()
	byName := r.stats.ToMap()
	views := make([]ReportComment, 0, n)
	for i := uint64(0); i < n; i++ {
		comment := r.comments[i]
		number := i + 1
		views = append(views, ReportComment{
			CommentView: comment.ToView(number, r.Timezone, r.CommentBodyConverter),
			Stats:       byName[comment.Author].ToView(number, r.Timezone),
		})
	}
	return views
}

// Len returns the number of comments without having to run Comments.
func (r Report) Len() uint64 {
	return uint64(len(r.comments))
}
