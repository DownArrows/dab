package main

import (
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"html"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// For the moment being, those interfaces are only used for documentation purpose.

type ReadableModel interface {
	FromDB(*sqlite.Stmt) error
}

type PersistentModel interface {
	ReadableModel
	InitializationQueries() []SQLQuery
	ToDB() []interface{}
}

type Comment struct {
	ID        string
	Author    string
	Score     int64
	Permalink string
	Sub       string
	Created   time.Time
	Body      string
}

func (c Comment) InitializationQueries() []SQLQuery {
	return []SQLQuery{
		{SQL: `CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			author TEXT NOT NULL,
			score INTEGER NOT NULL,
			permalink TEXT NOT NULL,
			sub TEXT NOT NULL,
			created INTEGER NOT NULL,
			body TEXT NOT NULL,
			FOREIGN KEY (author) REFERENCES user_archive(name)
		) WITHOUT ROWID`},
		{SQL: "CREATE INDEX IF NOT EXISTS comments_stats_idx ON comments (created)"},
		{SQL: `CREATE TRIGGER IF NOT EXISTS purge_user BEFORE DELETE ON user_archive
		BEGIN
			DELETE FROM comments WHERE author = OLD.name COLLATE NOCASE;
		END`},
	}
}

func (c Comment) ToDB() []interface{} {
	return []interface{}{c.ID, c.Author, c.Score, c.Permalink, c.Sub, c.Created.Unix(), c.Body}
}

func (c *Comment) FromDB(stmt *sqlite.Stmt) error {
	var err error

	if c.ID, _, err = stmt.ColumnText(0); err != nil {
		return err
	}

	if c.Author, _, err = stmt.ColumnText(1); err != nil {
		return err
	}

	if c.Score, _, err = stmt.ColumnInt64(2); err != nil {
		return err
	}

	if c.Permalink, _, err = stmt.ColumnText(3); err != nil {
		return err
	}

	if c.Sub, _, err = stmt.ColumnText(4); err != nil {
		return err
	}

	if timestamp, _, err := stmt.ColumnInt64(5); err != nil {
		return err
	} else {
		c.Created = time.Unix(timestamp, 0)
	}

	if body, _, err := stmt.ColumnText(6); err != nil {
		return err
	} else {
		c.Body = html.UnescapeString(body)
	}

	return err
}

func (c Comment) ToView(n uint, timezone *time.Location, cbc CommentBodyConverter) CommentView {
	view := CommentView{
		Number:        n,
		BodyConverter: cbc,
	}
	view.Comment = c
	view.Created = view.Created.In(timezone)
	return view
}

type CommentBodyConverter func(CommentView) (interface{}, error)

type CommentView struct {
	Comment
	BodyConverter CommentBodyConverter
	Number        uint
}

func (cv CommentView) BodyLines() []string {
	return strings.Split(cv.Body, "\n")
}

func (cv CommentView) BodyConvert() (interface{}, error) {
	if cv.BodyConverter != nil {
		return cv.BodyConverter(cv)
	}
	return cv.Body, nil
}

type User struct {
	Name      string
	Created   time.Time
	NotFound  bool
	Suspended bool

	Added     time.Time
	BatchSize uint
	Deleted   bool
	Hidden    bool
	Inactive  bool
	LastScan  time.Time
	New       bool
	Position  string
}

func (u User) InitializationQueries() []SQLQuery {
	return []SQLQuery{
		{SQL: `CREATE TABLE IF NOT EXISTS user_archive (
			name TEXT PRIMARY KEY,
			created INTEGER NOT NULL,
			not_found BOOLEAN DEFAULT FALSE NOT NULL,
			suspended BOOLEAN DEFAULT FALSE NOT NULL,
			added INTEGER NOT NULL,
			batch_size INTEGER DEFAULT ` + strconv.Itoa(MaxRedditListingLength) + ` NOT NULL,
			deleted BOOLEAN DEFAULT FALSE NOT NULL,
			hidden BOOLEAN NOT NULL,
			inactive BOOLEAN DEFAULT FALSE NOT NULL,
			last_scan INTEGER DEFAULT FALSE NOT NULL,
			new BOOLEAN DEFAULT TRUE NOT NULL,
			position TEXT DEFAULT "" NOT NULL
		) WITHOUT ROWID`},
		{SQL: `CREATE INDEX IF NOT EXISTS user_archive_idx
				ON user_archive (deleted, last_scan, inactive, suspended, not_found, hidden)`},
		{SQL: `CREATE VIEW IF NOT EXISTS
			users(name, created, not_found, suspended, added, batch_size, deleted, hidden, inactive, last_scan, new, position)
		AS SELECT * FROM user_archive WHERE deleted IS FALSE`},
	}
}

func (u User) ToDB() []interface{} {
	return []interface{}{u.Name, u.Created.Unix(), u.NotFound, u.Suspended, u.Added.Unix(),
		int(u.BatchSize), u.Hidden, u.Inactive, u.LastScan.Unix(), u.New, u.Position}
}

func (u User) InTimezone(timezone *time.Location) User {
	u.Created = u.Created.In(timezone)
	u.Added = u.Added.In(timezone)
	u.LastScan = u.LastScan.In(timezone)
	return u
}

func (u *User) FromDB(stmt *sqlite.Stmt) error {
	var err error

	if u.Name, _, err = stmt.ColumnText(0); err != nil {
		return err
	}

	if timestamp, _, err := stmt.ColumnInt64(1); err != nil {
		return err
	} else {
		u.Created = time.Unix(timestamp, 0)
	}

	if boolean, _, err := stmt.ColumnInt(2); err != nil {
		return err
	} else {
		u.NotFound = (boolean == 1)
	}

	if boolean, _, err := stmt.ColumnInt(3); err != nil {
		return err
	} else {
		u.Suspended = (boolean == 1)
	}

	if timestamp, _, err := stmt.ColumnInt64(4); err != nil {
		return err
	} else {
		u.Added = time.Unix(timestamp, 0)
	}

	if size, _, err := stmt.ColumnInt(5); err != nil {
		return err
	} else {
		u.BatchSize = uint(size)
	}

	if boolean, _, err := stmt.ColumnInt(6); err != nil {
		return err
	} else {
		u.Deleted = (boolean == 1)
	}

	if boolean, _, err := stmt.ColumnInt(7); err != nil {
		return err
	} else {
		u.Hidden = (boolean == 1)
	}

	if boolean, _, err := stmt.ColumnInt(8); err != nil {
		return err
	} else {
		u.Inactive = (boolean == 1)
	}

	if timestamp, _, err := stmt.ColumnInt64(9); err != nil {
		return err
	} else {
		u.LastScan = time.Unix(timestamp, 0)
	}

	if boolean, _, err := stmt.ColumnInt(10); err != nil {
		return err
	} else {
		u.New = (boolean == 1)
	}

	u.Position, _, err = stmt.ColumnText(11)

	return err
}

type UserQuery struct {
	User   User
	Exists bool
	Error  error
}

// Helper for logging queries.
func (uq UserQuery) String() string {
	status := []string{"name: " + uq.User.Name}
	if uq.Exists {
		status = append(status, "exists")
	} else {
		status = append(status, "does not exist")
	}
	if uq.User.Hidden {
		status = append(status, "hidden")
	}
	if uq.User.Suspended {
		status = append(status, "suspended")
	}
	if uq.User.NotFound {
		status = append(status, "not found")
	}
	if uq.Error != nil {
		status = append(status, "error: "+uq.Error.Error())
	}
	return strings.Join(status, ", ")
}

type UserStats struct {
	Name    string  // User name
	Average float64 // Average karma for the time span considered
	Delta   int64   // Karma loss for the time span considered
	Count   uint64  // Number of comments made by that user
}

func (us *UserStats) FromDB(stmt *sqlite.Stmt) error {
	var err error

	if us.Name, _, err = stmt.ColumnText(0); err != nil {
		return err
	}

	if us.Average, _, err = stmt.ColumnDouble(1); err != nil {
		return err
	}

	if us.Delta, _, err = stmt.ColumnInt64(2); err != nil {
		return err
	}

	if count, _, err := stmt.ColumnInt64(3); err != nil {
		return err
	} else {
		us.Count = uint64(count)
	}

	return nil
}

type UserStatsMap map[string]UserStats // Maps user names to corresponding stats for faster lookup

func (usm UserStatsMap) DeltasToSummaries() StatsSummaries {
	return usm.toSummaries(func(us UserStats) int64 { return us.Delta })
}

func (usm UserStatsMap) AveragesToSummaries() StatsSummaries {
	return usm.toSummaries(func(us UserStats) int64 { return int64(math.Round(us.Average)) })
}

func (usm UserStatsMap) toSummaries(summary func(UserStats) int64) StatsSummaries {
	stats := make([]StatsSummary, 0, len(usm))
	for name, data := range usm {
		stats = append(stats, StatsSummary{
			Name:    name,
			Count:   data.Count,
			Summary: summary(data),
		})
	}
	return stats
}

// Abstract representation of a value corresponding to a statistical summary
// of a collection of things related to a user.
type StatsSummary struct {
	Name    string // User name
	Count   uint64 // Number of things considered
	Summary int64  // Summary number for the things considered
}

type StatsSummaries []StatsSummary

func (s StatsSummaries) Limit(limit uint) StatsSummaries {
	length := uint(len(s))
	if length > limit {
		length = limit
	}
	result := make([]StatsSummary, length)
	copy(result, s)
	return result
}

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

type CompendiumUserStats struct {
	All                  []*CompendiumUserStatsDetailsPerSub
	CommentBodyConverter CommentBodyConverter
	NbTop                uint
	Negative             []*CompendiumUserStatsDetailsPerSub
	RawTopComments       []Comment
	Summary              *CompendiumUserStatsDetails
	SummaryNegative      *CompendiumUserStatsDetails
	Timezone             *time.Location
	User                 User
	Version              SemVer
}

func (stats *CompendiumUserStats) PercentageNegative() int64 {
	if stats.Summary.Count == 0 || stats.SummaryNegative.Count == 0 {
		return 0
	}
	return int64(math.Round(100 * float64(stats.SummaryNegative.Count) / float64(stats.Summary.Count)))
}

func (stats *CompendiumUserStats) NbTopComments() int {
	return len(stats.RawTopComments)
}

func (stats *CompendiumUserStats) TopComments() []CommentView {
	views := make([]CommentView, 0, len(stats.RawTopComments))
	for i, comment := range stats.RawTopComments {
		view := comment.ToView(uint(i+1), stats.Timezone, stats.CommentBodyConverter)
		views = append(views, view)
	}
	return views
}

type CompendiumUserStatsDetails struct {
	Average float64
	Count   int64
	Karma   int64
	Latest  time.Time
	Number  uint
}

func (details *CompendiumUserStatsDetails) FromDB(stmt *sqlite.Stmt) error {
	var err error

	if details.Count, _, err = stmt.ColumnInt64(0); err != nil {
		return err
	}

	if details.Average, _, err = stmt.ColumnDouble(1); err != nil {
		return err
	}

	if details.Karma, _, err = stmt.ColumnInt64(2); err != nil {
		return err
	}

	if latest, _, err := stmt.ColumnInt64(3); err != nil {
		return err
	} else {
		details.Latest = time.Unix(latest, 0)
	}

	return nil
}

func (details *CompendiumUserStatsDetails) KarmaPerComment() float64 {
	if details.Count == 0 || details.Karma == 0 {
		return 0
	}
	return float64(math.Round(float64(details.Karma) / float64(details.Count)))
}

type CompendiumUserStatsDetailsPerSub struct {
	CompendiumUserStatsDetails
	Sub string
}

func (details *CompendiumUserStatsDetailsPerSub) FromDB(stmt *sqlite.Stmt) error {
	if err := details.CompendiumUserStatsDetails.FromDB(stmt); err != nil {
		return err
	}
	sub, _, err := stmt.ColumnText(4)
	details.Sub = sub
	return err
}
