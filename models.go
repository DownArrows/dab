package main

import (
	"html"
	"math"
	"strconv"
	"strings"
	"time"
)

// ReadableModel is a data structure that can be read from a database.
// Only defined for documentation purposes.
type ReadableModel interface {
	FromDB(*SQLiteStmt) error
}

// PersistentModel is a data structure that can be read from and written to a database.
// Only defined for documentation purposes.
type PersistentModel interface {
	ReadableModel
	InitializationQueries() []SQLQuery
	ToDB() []interface{}
}

// Comment is a Reddit comment.
type Comment struct {
	ID        string
	Author    string
	Score     int64
	Permalink string
	Sub       string
	Created   time.Time
	Body      string
}

// InitializationQueries returns SQL queries to store Comments.
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

// ToDB returns arguments in the correct order to register a Comment.
func (c Comment) ToDB() []interface{} {
	return []interface{}{c.ID, c.Author, c.Score, c.Permalink, c.Sub, c.Created.Unix(), c.Body}
}

// FromDB reads a comment from a database.
func (c *Comment) FromDB(stmt *SQLiteStmt) error {
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

	var timestamp int64
	if timestamp, _, err = stmt.ColumnInt64(5); err != nil {
		return err
	}
	c.Created = time.Unix(timestamp, 0)

	var body string
	if body, _, err = stmt.ColumnText(6); err != nil {
		return err
	}
	c.Body = html.UnescapeString(body)

	return nil
}

// ToView converts the comment to a data structure suitable for use in a template.
func (c Comment) ToView(n uint, timezone *time.Location, cbc CommentBodyConverter) CommentView {
	view := CommentView{
		Number:        n,
		BodyConverter: cbc,
	}
	view.Comment = c
	view.Created = view.Created.In(timezone)
	return view
}

// CommentBodyConverter is a function that converts a comment's body to a suitable format for use in a template (eg. HTML).
type CommentBodyConverter func(CommentView) (interface{}, error)

// CommentView is a data structure describing a Comment such as it is suitable for use in a template.
type CommentView struct {
	Comment
	BodyConverter CommentBodyConverter
	Number        uint
}

// BodyLines returns the lines in the comment of a Comment.
func (cv CommentView) BodyLines() []string {
	return strings.Split(cv.Body, "\n")
}

// BodyConvert returns the converted comment; if the converter isn't set, it returns the raw Comment.Body.
func (cv CommentView) BodyConvert() (interface{}, error) {
	if cv.BodyConverter != nil {
		return cv.BodyConverter(cv)
	}
	return cv.Body, nil
}

// User describes a Reddit user.
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

// InitializationQueries retuns the SQL queries to create a table to save the User data structure.
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

// ToDB returns well-ordered arguments to save a User.
func (u User) ToDB() []interface{} {
	return []interface{}{u.Name, u.Created.Unix(), u.NotFound, u.Suspended, u.Added.Unix(),
		int(u.BatchSize), u.Hidden, u.Inactive, u.LastScan.Unix(), u.New, u.Position}
}

// InTimezone converts the User's dates to the given time zone.
func (u User) InTimezone(timezone *time.Location) User {
	u.Created = u.Created.In(timezone)
	u.Added = u.Added.In(timezone)
	u.LastScan = u.LastScan.In(timezone)
	return u
}

// FromDB reads a User from a database.
func (u *User) FromDB(stmt *SQLiteStmt) error {
	var err error
	var timestamp int64
	var boolean int

	if u.Name, _, err = stmt.ColumnText(0); err != nil {
		return err
	}

	if timestamp, _, err = stmt.ColumnInt64(1); err != nil {
		return err
	}
	u.Created = time.Unix(timestamp, 0)

	if boolean, _, err = stmt.ColumnInt(2); err != nil {
		return err
	}
	u.NotFound = (boolean == 1)

	if boolean, _, err = stmt.ColumnInt(3); err != nil {
		return err
	}
	u.Suspended = (boolean == 1)

	if timestamp, _, err = stmt.ColumnInt64(4); err != nil {
		return err
	}
	u.Added = time.Unix(timestamp, 0)

	var size int
	if size, _, err = stmt.ColumnInt(5); err != nil {
		return err
	}
	u.BatchSize = uint(size)

	if boolean, _, err = stmt.ColumnInt(6); err != nil {
		return err
	}
	u.Deleted = (boolean == 1)

	if boolean, _, err = stmt.ColumnInt(7); err != nil {
		return err
	}
	u.Hidden = (boolean == 1)

	if boolean, _, err = stmt.ColumnInt(8); err != nil {
		return err
	}
	u.Inactive = (boolean == 1)

	if timestamp, _, err = stmt.ColumnInt64(9); err != nil {
		return err
	}
	u.LastScan = time.Unix(timestamp, 0)

	if boolean, _, err = stmt.ColumnInt(10); err != nil {
		return err
	}
	u.New = (boolean == 1)

	u.Position, _, err = stmt.ColumnText(11)

	return err
}

// UserQuery describes a query to register or read a User.
type UserQuery struct {
	User   User
	Exists bool
	Error  error
}

// StatsRead tells which optional fields should be read from an SQL statement when populating a Stats data structure.
type StatsRead struct {
	Start  uint
	Name   bool
	Latest bool
}

// Stats describes the statistical data that is presented by the application.
type Stats struct {
	Count   uint64
	Sum     int64
	Average float64
	Name    string
	Latest  time.Time
}

// FromDB reads the statistics from the results of a relevant SQL query.
func (s *Stats) FromDB(stmt *SQLiteStmt, read StatsRead) error {
	var err error
	pos := int(read.Start)

	var count int64
	if count, _, err = stmt.ColumnInt64(pos); err != nil {
		return err
	}
	s.Count = uint64(count)
	pos++

	if s.Sum, _, err = stmt.ColumnInt64(pos); err != nil {
		return err
	}
	pos++

	if s.Average, _, err = stmt.ColumnDouble(pos); err != nil {
		return err
	}
	pos++

	if read.Name {
		if s.Name, _, err = stmt.ColumnText(pos); err != nil {
			return err
		}
		pos++
	}

	if read.Latest {
		var latest int64
		if latest, _, err = stmt.ColumnInt64(pos); err != nil {
			return err
		}
		s.Latest = time.Unix(latest, 0)
		pos++
	}

	return nil
}

// ToView converts the Stats to a data structure suitable for use in a template.
func (s Stats) ToView(n uint, timezone *time.Location) StatsView {
	view := StatsView{Stats: s, Number: n}
	view.Average = math.Round(view.Average)
	if timezone != nil {
		view.Latest = view.Latest.In(timezone)
	}
	return view
}

// StatsCollection is a slice of Stats onto which specific operations can be made.
type StatsCollection []Stats

// ToMap retuns a map that associates a name with statistics.
func (sc StatsCollection) ToMap() map[string]Stats {
	data := make(map[string]Stats)
	for _, stats := range sc {
		data[stats.Name] = stats
	}
	return data
}

// ToView converts every the Stats to data structures suitable for use in a template.
func (sc StatsCollection) ToView(timezone *time.Location) []StatsView {
	views := make([]StatsView, 0, len(sc))
	for n, stats := range sc {
		views = append(views, stats.ToView(uint(n+1), timezone))
	}
	return views
}

// Stats returns global Stats about the collection.
func (sc StatsCollection) Stats() Stats {
	count := sc.Count()
	sum := sc.Sum()
	return Stats{
		Count:   count,
		Sum:     sum,
		Average: float64(sum) / float64(count),
		Latest:  sc.Latest(),
	}
}

// Count is the global count.
func (sc StatsCollection) Count() uint64 {
	var count uint64
	for _, stats := range sc {
		count += stats.Count
	}
	return count
}

// Sum is the global sum.
func (sc StatsCollection) Sum() int64 {
	var sum int64
	for _, stats := range sc {
		sum += stats.Sum
	}
	return sum
}

// Latest is the global most recent time.
func (sc StatsCollection) Latest() time.Time {
	var latest time.Time
	for _, stats := range sc {
		if stats.Latest.After(latest) {
			latest = stats.Latest
		}
	}
	return latest
}

// Filter makes a copy of the collection without the elements for which the callback returns false.
func (sc StatsCollection) Filter(filter func(Stats) bool) StatsCollection {
	length := len(sc)
	out := make(StatsCollection, 0, length)
	for _, stats := range sc {
		if filter(stats) {
			out = append(out, stats)
		}
	}
	return out
}

// OrderByAverage orders the collection by average.
func (sc StatsCollection) OrderByAverage() StatsCollection {
	length := len(sc)
	out := make(StatsCollection, length)
	copy(out, sc)
	Sort{
		Len:  func() int { return length },
		Less: func(i, j int) bool { return out[i].Average < out[j].Average },
		Swap: func(i, j int) { out[i], out[j] = out[j], out[i] },
	}.Do()
	return out
}

// OrderBySum orders the collection by sums.
func (sc StatsCollection) OrderBySum() StatsCollection {
	length := len(sc)
	out := make(StatsCollection, length)
	copy(out, sc)
	Sort{
		Len:  func() int { return length },
		Less: func(i, j int) bool { return out[i].Sum < out[j].Sum },
		Swap: func(i, j int) { out[i], out[j] = out[j], out[i] },
	}.Do()
	return out
}

// Limit clips the collection to a limit.
func (sc StatsCollection) Limit(limit uint) StatsCollection {
	length := uint(len(sc))
	if length > limit {
		length = limit
	}
	out := make(StatsCollection, length)
	copy(out, sc)
	return out
}

// StatsView is a data structure describing Stats such as it is suitable for use in a template.
type StatsView struct {
	Stats
	Number uint
}
