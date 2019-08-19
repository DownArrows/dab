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

// ReadableModel is a data structure that can be read from a database.
// Only defined for documentation purposes.
type ReadableModel interface {
	FromDB(*sqlite.Stmt) error
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
func (u *User) FromDB(stmt *sqlite.Stmt) error {
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

// String is a helper for logging queries.
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

// UserStats describes the commenting statistics of a User over a certain period of time.
type UserStats struct {
	Name    string  // User name
	Average float64 // Average karma for the time span considered
	Delta   int64   // Karma loss for the time span considered
	Count   uint64  // Number of comments made by that user
}

// FromDB reads the statistics from the results of a relevant SQL query.
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

	var count int64
	if count, _, err = stmt.ColumnInt64(3); err != nil {
		return err
	}
	us.Count = uint64(count)

	return nil
}

// UserStatsMap  maps user names to corresponding their UserStats for faster lookup.
type UserStatsMap map[string]UserStats

// DeltasToSummaries converts all the UserStats to statistical summaries where the summary is the UserStats.Delta.
func (usm UserStatsMap) DeltasToSummaries() StatsSummaries {
	return usm.toSummaries(func(us UserStats) int64 { return us.Delta })
}

// AveragesToSummaries converts all the UserStats to statistical summaries where the summary is the UserStats.Average.
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

// StatsSummary is an abstract representation of a value corresponding
// to a statistical summary of a collection of things related to a User.
type StatsSummary struct {
	Name    string // User name
	Count   uint64 // Number of things considered
	Summary int64  // Summary number for the things considered
}

// StatsSummaries is a slice of StatsSummaries that has custom operations.
type StatsSummaries []StatsSummary

// Limit returns a StatsSummaries clipped the given maximum of elements.
func (s StatsSummaries) Limit(limit uint) StatsSummaries {
	length := uint(len(s))
	if length > limit {
		length = limit
	}
	result := make([]StatsSummary, length)
	copy(result, s)
	return result
}

// Len returns the length of the collection of StatsSummary.
func (s StatsSummaries) Len() int {
	return len(s)
}

// Less returns whether the StatsSummary in position i is logically before the one in position j.
func (s StatsSummaries) Less(i, j int) bool {
	return s[i].Summary < s[j].Summary
}

// Swap swaps the StatsSummary in position i with the one in position j.
func (s StatsSummaries) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// Sort sorts the StatsSummaries according to their Summary field.
func (s StatsSummaries) Sort() StatsSummaries {
	sort.Sort(s)
	return s
}

// CompendiumDetails describes a single item in a compendium page.
type CompendiumDetails struct {
	Average float64
	Count   int64
	Karma   int64
	Latest  time.Time
	Number  uint
}

// FromDB reads a CompendiumDetails from the relevant SQL query.
func (details *CompendiumDetails) FromDB(stmt *sqlite.Stmt) error {
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

	var latest int64
	if latest, _, err = stmt.ColumnInt64(3); err != nil {
		return err
	}
	details.Latest = time.Unix(latest, 0)

	return nil
}

// Normalize makes the data suitable suitable for use in a view.
// n sets the position in the collection (set to 0 if not relevant), and timezone sets the time zone of the dates.
func (details *CompendiumDetails) Normalize(n uint, timezone *time.Location) {
	details.Average = math.Round(details.Average)
	details.Latest = details.Latest.In(timezone)
	details.Number = n
}

// KarmaPerComment gives the average karma score per comment that has been counted.
func (details *CompendiumDetails) KarmaPerComment() float64 {
	if details.Count == 0 || details.Karma == 0 {
		return 0
	}
	return float64(math.Round(float64(details.Karma) / float64(details.Count)))
}

// CompendiumDetailsTagged is a variant of the CompendiumDetails with a string tag.
type CompendiumDetailsTagged struct {
	CompendiumDetails
	Tag string
}

// FromDB reads a CompendiumDetailsTagged from the relevant SQL query.
func (details *CompendiumDetailsTagged) FromDB(stmt *sqlite.Stmt) error {
	if err := details.CompendiumDetails.FromDB(stmt); err != nil {
		return err
	}
	tag, _, err := stmt.ColumnText(4)
	details.Tag = tag
	return err
}
