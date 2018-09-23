package main

import (
	"database/sql"
	"fmt"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"time"
)

type RedditBotStorage interface {
	// Users
	AddUser(string, bool, time.Time) error
	GetUser(string) UserQuery
	ListUsers() []User
	ListSuspended() []User
	ListActiveUsers() []User
	SuspendUser(string) error
	UnSuspendUser(string) error
	NotNewUser(string) error
	ResetPosition(string) error
	// Comments
	SaveCommentsPage([]Comment, User) error
	UpdateInactiveStatus(time.Duration) error
	// Key-value
	SeenPostIDs(string) []string
	SaveSubPostIDs([]Comment, string) error
	IsKnownObject(string) bool
	SaveKnownObject(string) error
}

type DiscordBotStorage interface {
	DelUser(string) error
	PurgeUser(string) error
	HideUser(string) error
	UnHideUser(string) error
	GetUser(string) UserQuery
	GetTotalKarma(string) (int64, error)
	GetNegativeKarma(string) (int64, error)
}

type ReportFactoryStorage interface {
	GetCommentsBelowBetween(int64, time.Time, time.Time) []Comment
	StatsBetween(time.Time, time.Time) UserStatsMap
}

type Storage struct {
	client          *sql.DB
	db              *sqlx.DB
	Path            string
	CleanupInterval time.Duration
}

func NewStorage(conf StorageConf) *Storage {
	s := &Storage{
		Path:            conf.Path,
		CleanupInterval: conf.CleanupInterval.Value,
	}
	if s.CleanupInterval < 1*time.Minute && s.CleanupInterval != 0*time.Second {
		panic("database cleanup interval can't be under a minute if superior to 0s")
	}

	s.db = sqlx.MustConnect("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=1&cache=shared", s.Path))

	s.Init()
	s.launchPeriodicVacuum()

	return s
}

func (s *Storage) Init() {
	s.db.SetMaxOpenConns(1)

	if s.Path != ":memory:" {
		s.EnableWAL()
	}

	s.db.MustExec(`
		CREATE TABLE IF NOT EXISTS tracked (
			name TEXT PRIMARY KEY,
			created TIMESTAMP NOT NULL,
			inactive BOOLEAN DEFAULT 0 NOT NULL,
			suspended BOOLEAN DEFAULT 0 NOT NULL,
			deleted BOOLEAN DEFAULT 0 NOT NULL,
			added TIMESTAMP NOT NULL,
			hidden BOOLEAN NOT NULL,
			new BOOLEAN DEFAULT 1 NOT NULL,
			position TEXT DEFAULT "" NOT NULL
		) WITHOUT ROWID`)
	s.db.MustExec(`
		CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			author TEXT NOT NULL,
			score INTEGER NOT NULL,
			permalink TEXT NOT NULL,
			sub TEXT NOT NULL,
			created TIMESTAMP NOT NULL,
			body TEXT NOT NULL,
			FOREIGN KEY (author) REFERENCES tracked(name)
		) WITHOUT ROWID`)
	s.db.MustExec(`
		CREATE TABLE IF NOT EXISTS seen_posts (
			id TEXT PRIMARY KEY,
			sub TEXT NOT NULL,
			created TIMESTAMP NOT NULL
		) WITHOUT ROWID`)
	s.db.MustExec(`
		CREATE TABLE IF NOT EXISTS known_objects (
			id TEXT PRIMARY KEY,
			date TIMESTAMP NOT NULL
		) WITHOUT ROWID`)
	s.db.MustExec(`
		CREATE VIEW IF NOT EXISTS
			users(name, created, added, suspended, hidden, new, position, inactive)
		AS
			SELECT name, created, added, suspended, hidden, new, position, inactive
			FROM tracked WHERE deleted = 0`)
}

func (s *Storage) launchPeriodicVacuum() {
	if s.CleanupInterval == 0*time.Second {
		return
	}

	go func() {
		for {
			time.Sleep(s.CleanupInterval)
			s.db.MustExec("VACUUM")
		}
	}()
}

func (s *Storage) Close() {
	autopanic(s.db.Close())
}

func (s *Storage) EnableWAL() {
	var journal_mode string
	autopanic(s.db.Get(&journal_mode, "PRAGMA journal_mode=WAL"))
	if journal_mode != "wal" {
		autopanic(fmt.Errorf("failed to set journal mode to Write-Ahead Log (WAL)"))
	}
}

/*****
 Users
******/

func (s *Storage) AddUser(username string, hidden bool, created time.Time) error {
	stmt, err := s.db.Prepare(`
		INSERT INTO tracked(name, hidden, created, added)
		VALUES (?, ?, ?, strftime("%s", CURRENT_TIMESTAMP))`)
	autopanic(err)
	defer stmt.Close()
	_, err = stmt.Exec(username, hidden, created.Unix())
	return err
}

func (s *Storage) GetUser(username string) UserQuery {
	query := UserQuery{User: User{Name: username}}
	sql_query := `SELECT * FROM users WHERE name = ? COLLATE NOCASE`
	err := s.db.QueryRowx(sql_query, username).StructScan(&query.User)
	if err == sql.ErrNoRows {
		query.Exists = false
	} else if err != nil {
		query.Error = err
	} else {
		query.Exists = true
	}
	return query
}

func (s *Storage) simpleEditUser(query, username string) error {
	r := s.db.MustExec(query, username)
	if nb, _ := r.RowsAffected(); nb == 0 {
		return fmt.Errorf("no user named '%s'", username)
	}
	return nil
}

func (s *Storage) DelUser(username string) error {
	return s.simpleEditUser("UPDATE tracked SET deleted = 1 WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) HideUser(username string) error {
	return s.simpleEditUser("UPDATE tracked SET hidden = 1 WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) UnHideUser(username string) error {
	return s.simpleEditUser("UPDATE tracked SET hidden = 0 WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) SuspendUser(username string) error {
	return s.simpleEditUser("UPDATE tracked SET suspended = 1 WHERE name = ?", username)
}

func (s *Storage) UnSuspendUser(username string) error {
	return s.simpleEditUser("UPDATE tracked SET suspended = 0 WHERE name = ?", username)
}

func (s *Storage) NotNewUser(username string) error {
	return s.simpleEditUser("UPDATE tracked SET new = 0 WHERE name = ?", username)
}

func (s *Storage) ResetPosition(username string) error {
	return s.simpleEditUser("UPDATE tracked SET position = '' WHERE name = ?", username)
}

func (s *Storage) PurgeUser(username string) error {
	tx := s.db.MustBegin()

	// This must happen before deleting the user due to the foreign key constraints
	tx.MustExec("DELETE FROM comments WHERE author = ? COLLATE NOCASE", username)
	r := tx.MustExec("DELETE FROM tracked WHERE name = ? COLLATE NOCASE", username)
	if nb, _ := r.RowsAffected(); nb == 0 {
		tx.Rollback()
		return fmt.Errorf("no user named '%s'", username)
	}

	return tx.Commit()
}

func (s *Storage) anyListUsers(q string) []User {
	var users []User
	err := s.db.Select(&users, q)
	autopanic(err)
	return users
}

func (s *Storage) ListUsers() []User {
	return s.anyListUsers("SELECT * FROM users WHERE suspended = 0 ORDER BY name")
}

func (s *Storage) ListSuspended() []User {
	return s.anyListUsers("SELECT * FROM users WHERE suspended = 1 ORDER BY name")
}

func (s *Storage) ListActiveUsers() []User {
	return s.anyListUsers("SELECT * FROM users WHERE inactive = 0 AND suspended = 0 ORDER BY name")
}

func (s *Storage) UpdateInactiveStatus(max_age time.Duration) error {
	// We use two SQL statements instead of one because SQLite is too limited
	// to do that in a single statement that isn't exceedingly complicated.
	template := `
		UPDATE tracked SET inactive = ?
		WHERE name IN (
			SELECT author FROM (
				SELECT author, max(created) AS last
				FROM comments GROUP BY author
			) WHERE (? - last) %s ?
		)`
	tx := s.db.MustBegin()
	now := time.Now().Round(0).Unix()
	tx.MustExec(fmt.Sprintf(template, ">"), 1, now, max_age.Seconds())
	tx.MustExec(fmt.Sprintf(template, "<"), 0, now, max_age.Seconds())
	return tx.Commit()
}

/********
 Comments
*********/

// Make sure the comments are all from the same user and its struct is up to date
func (s *Storage) SaveCommentsPage(comments []Comment, user User) error {
	tx := s.db.MustBegin()

	stmt, err := tx.PrepareNamed(`
		INSERT INTO comments VALUES (:id, :author, :score, :permalink, :sub, :created, :body)
		ON CONFLICT(id) DO UPDATE SET
			score=excluded.score,
			body=excluded.body
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, comment := range comments {
		stmt.MustExec(comment)
	}

	// We don't need to check for an error because if the user doesn't exist,
	// then the constraints would have make the previous statement fail.
	tx.MustExec("UPDATE tracked SET position = ? WHERE name = ?", user.Position, user.Name)

	return tx.Commit()
}

func (s *Storage) GetCommentsBelowBetween(score int64, since, until time.Time) []Comment {
	q := `SELECT
			comments.id, comments.author, comments.score, comments.sub,
			comments.permalink, comments.body, comments.created
		FROM comments JOIN users
		ON comments.author = users.name
		WHERE
			comments.score <= ?
			AND users.hidden = 0
			AND comments.created BETWEEN ? AND ?
		ORDER BY comments.score ASC`
	var comments []Comment
	err := s.db.Select(&comments, q, score, since.Unix(), until.Unix())
	if err != nil && err != sql.ErrNoRows {
		panic(err)
	}
	return comments
}

/**********
 Statistics
***********/

func (s *Storage) GetTotalKarma(username string) (int64, error) {
	return s.getKarma("SELECT SUM(score) FROM comments WHERE author = ? COLLATE NOCASE", username)
}

func (s *Storage) GetNegativeKarma(username string) (int64, error) {
	return s.getKarma("SELECT SUM(score) FROM comments WHERE score < 0 AND author = ?", username)
}

func (s *Storage) getKarma(q, username string) (int64, error) {
	var score int64
	err := s.db.Get(&score, q, username)
	if err != nil {
		return 0, err
	} else if err == sql.ErrNoRows {
		return 0, fmt.Errorf("no comments from user '%s' found", username)
	}
	return score, nil
}

func (s *Storage) StatsBetween(since, until time.Time) UserStatsMap {
	stmt, err := s.db.Prepare(`
		SELECT
			comments.author AS author,
			AVG(comments.score) AS average,
			SUM(comments.score) AS delta,
			COUNT(comments.id) AS count
		FROM comments JOIN users
		ON comments.author = users.name
		WHERE
			score < 0
			AND users.hidden = 0
			AND comments.created BETWEEN ? AND ?
		GROUP BY comments.author`)
	autopanic(err)
	defer stmt.Close()

	rows, err := stmt.Query(since.Unix(), until.Unix())
	autopanic(err)
	defer rows.Close()

	var stats = UserStatsMap{}
	for rows.Next() {
		var data UserStats
		autopanic(rows.Scan(&data.Name, &data.Average, &data.Delta, &data.Count))
		stats[data.Name] = data
	}
	autopanic(rows.Err())

	return stats
}

/*****
 Posts
******/

func (s *Storage) SaveSubPostIDs(listing []Comment, sub string) error {
	tx := s.db.MustBegin()
	stmt, err := tx.Preparex(`
		INSERT INTO seen_posts(id, sub, created) VALUES (?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, post := range listing {
		stmt.MustExec(post.Id, sub, post.Created.Unix())
	}

	return tx.Commit()
}

func (s *Storage) SeenPostIDs(sub string) []string {
	var ids []string
	err := s.db.Select(&ids, "SELECT id FROM seen_posts WHERE sub = ?", sub)
	if err != nil && err != sql.ErrNoRows {
		panic(err)
	}
	return ids
}

func (s *Storage) SaveKnownObject(id string) error {
	_, err := s.db.Exec("INSERT INTO known_objects VALUES (?, ?)", id, time.Now())
	return err
}

func (s *Storage) IsKnownObject(id string) bool {
	var result string
	err := s.db.Get(&result, "SELECT id FROM known_objects WHERE id = ?", id)
	if err == sql.ErrNoRows {
		return false
	}
	autopanic(err)
	return true
}
