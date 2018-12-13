package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jmoiron/sqlx"
	"github.com/mattn/go-sqlite3"
	"sync"
	"time"
)

type RedditScannerStorage interface {
	KnownObjects
	ListActiveUsers() []User
	ListSuspendedAndNotFound() []User
	ListUsers() []User
	SaveCommentsUpdateUser([]Comment, User, time.Duration) (User, error)
	UpdateInactiveStatus(time.Duration) error
}

type RedditUsersStorage interface {
	KnownObjects
	AddUser(string, bool, int64) error
	FoundUser(string) error
	GetUser(string) UserQuery
	ListSuspendedAndNotFound() []User
	NotFoundUser(string) error
	SuspendUser(string) error
	UnSuspendUser(string) error
}

type RedditSubsStorage interface {
	IsKnownSubPostID(string, string) bool
	NbKnownPostIDs(string) int
	SaveSubPostIDs(string, []Comment) error
}

type DiscordBotStorage interface {
	DelUser(string) error
	PurgeUser(string) error
	HideUser(string) error
	UnHideUser(string) error
	GetUser(string) UserQuery
	GetPositiveKarma(string) (int64, error)
	GetNegativeKarma(string) (int64, error)
}

type ReportFactoryStorage interface {
	GetCommentsBelowBetween(int64, time.Time, time.Time) []Comment
	StatsBetween(time.Time, time.Time) UserStatsMap
}

type BackupStorage interface {
	Backup() error
	BackupPath() string
}

type KnownObjects interface {
	IsKnownObject(string) bool
	SaveKnownObject(string) error
}

var ErrNoComment = errors.New("no comment found")

const DatabaseDriverName = "sqlite3_dab"

var connections = make(chan *sqlite3.SQLiteConn, 1)

func init() {
	sql.Register(DatabaseDriverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			connections <- conn
			return nil
		},
	})
}

var initQueries = []string{
	fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS user_archive (
			name TEXT PRIMARY KEY,
			created INTEGER NOT NULL,
			not_found BOOLEAN DEFAULT 0 NOT NULL,
			suspended BOOLEAN DEFAULT 0 NOT NULL,
			added INTEGER NOT NULL,
			batch_size INTEGER DEFAULT %d NOT NULL,
			deleted BOOLEAN DEFAULT 0 NOT NULL,
			hidden BOOLEAN NOT NULL,
			inactive BOOLEAN DEFAULT 0 NOT NULL,
			new BOOLEAN DEFAULT 1 NOT NULL,
			position TEXT DEFAULT "" NOT NULL
		) WITHOUT ROWID`, MaxRedditListingLength),
	`CREATE INDEX IF NOT EXISTS user_archive_idx
			ON user_archive (deleted, inactive, suspended, not_found, hidden)`,
	`CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			author TEXT NOT NULL,
			score INTEGER NOT NULL,
			permalink TEXT NOT NULL,
			sub TEXT NOT NULL,
			created INTEGER NOT NULL,
			body TEXT NOT NULL,
			FOREIGN KEY (author) REFERENCES user_archive(name)
		) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS comments_stats_idx
			ON comments (created)`,
	`CREATE TABLE IF NOT EXISTS seen_posts (
			id TEXT PRIMARY KEY,
			sub TEXT NOT NULL,
			created INTEGER NOT NULL
		) WITHOUT ROWID`,
	`CREATE TABLE IF NOT EXISTS known_objects (
			id TEXT PRIMARY KEY,
			date INTEGER NOT NULL
		) WITHOUT ROWID`,
	`CREATE VIEW IF NOT EXISTS
			users(name, created, not_found, suspended, added, batch_size, hidden, inactive, new, position)
		AS
			SELECT name, created, not_found, suspended, added, batch_size, hidden, inactive, new, position
			FROM user_archive WHERE deleted = 0`,
}

type storageBackup struct {
	sync.Mutex
	Path   string
	MaxAge time.Duration
}

type Storage struct {
	backup          storageBackup
	cleanupInterval time.Duration
	conn            *sqlite3.SQLiteConn
	db              *sqlx.DB
	path            string
	cache           struct {
		KnownObjects   *SyncSet
		SubPostIDs     map[string]*SyncSet
		SubPostIDsLock sync.Mutex
	}

	PeriodicVacuumEnabled bool
}

func NewStorage(conf StorageConf) (*Storage, error) {
	s := &Storage{
		cleanupInterval: conf.CleanupInterval.Value,
		path:            conf.Path,

		backup: storageBackup{
			Path:   conf.BackupPath,
			MaxAge: conf.BackupMaxAge.Value,
		},

		PeriodicVacuumEnabled: conf.CleanupInterval.Value > 0,
	}

	db, err := sql.Open(DatabaseDriverName, fmt.Sprintf("file:%s?_foreign_keys=1&cache=shared", s.path))
	if err != nil {
		return nil, err
	}
	s.db = sqlx.NewDb(db, "sqlite3")

	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()

	// trigger connection hook
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s.conn = <-connections

	s.db.SetMaxOpenConns(1)

	if s.path != ":memory:" {
		if err := s.enableWAL(); err != nil {
			return nil, err
		}
	}

	for _, query := range initQueries {
		if _, err := s.db.Exec(query); err != nil {
			return nil, err
		}
	}

	s.cache.KnownObjects = NewSyncSet()
	known_objects, err := s.getKnownObjects()
	if err != nil {
		return nil, err
	}
	s.cache.KnownObjects.MultiPut(known_objects)
	s.cache.SubPostIDs = make(map[string]*SyncSet)
	s.cache.SubPostIDsLock = sync.Mutex{}

	ok = true
	return s, nil
}

func (s *Storage) enableWAL() error {
	var journal_mode string
	if err := s.db.Get(&journal_mode, "PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if journal_mode != "wal" {
		return errors.New("failed to set journal mode to Write-Ahead Log (WAL)")
	}
	return nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

/***********
 Maintenance
************/

func (s *Storage) PeriodicVacuum(ctx context.Context) error {
	for sleepCtx(ctx, s.cleanupInterval) {
		if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *Storage) Backup() error {
	s.backup.Lock()
	defer s.backup.Unlock()

	if older, err := fileOlderThan(s.backup.Path, s.backup.MaxAge); err != nil {
		return err
	} else if !older {
		return nil
	}

	dest_db, err := sql.Open(DatabaseDriverName, "file:"+s.backup.Path)
	if err != nil {
		return err
	}
	defer dest_db.Close()

	// trigger the connection hook
	if err := dest_db.Ping(); err != nil {
		return err
	}
	dest_conn := <-connections

	backup, err := dest_conn.Backup("main", s.conn, "main") // yes, in *that* order!
	if err != nil {
		return err
	}
	defer backup.Close()

	for done := false; !done; {
		done, err = backup.Step(-1) // -1 means to save all remaining pages
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Storage) BackupPath() string {
	return s.backup.Path
}

/*****
 Users
******/

func (s *Storage) AddUser(username string, hidden bool, created int64) error {
	stmt, err := s.db.Prepare(`
		INSERT INTO user_archive(name, hidden, created, added)
		VALUES (?, ?, ?, strftime("%s", CURRENT_TIMESTAMP))`)
	autopanic(err)
	defer stmt.Close()
	_, err = stmt.Exec(username, hidden, created)
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
	return s.simpleEditUser("UPDATE user_archive SET deleted = 1 WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) HideUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET hidden = 1 WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) UnHideUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET hidden = 0 WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) SuspendUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET suspended = 1 WHERE name = ?", username)
}

func (s *Storage) UnSuspendUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET suspended = 0 WHERE name = ?", username)
}

func (s *Storage) NotFoundUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET not_found = 1 WHERE name = ?", username)
}

func (s *Storage) FoundUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET not_found = 0 WHERE name = ?", username)
}

func (s *Storage) PurgeUser(username string) error {
	tx := s.db.MustBegin()

	// This must happen before deleting the user due to the foreign key constraints
	tx.MustExec("DELETE FROM comments WHERE author = ? COLLATE NOCASE", username)
	r := tx.MustExec("DELETE FROM user_archive WHERE name = ? COLLATE NOCASE", username)
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
	return s.anyListUsers("SELECT * FROM users WHERE suspended = 0 AND not_found = 0 ORDER BY name")
}

func (s *Storage) ListSuspendedAndNotFound() []User {
	return s.anyListUsers("SELECT * FROM users WHERE suspended = 1 OR not_found = 1 ORDER BY name")
}

func (s *Storage) ListActiveUsers() []User {
	return s.anyListUsers("SELECT * FROM users WHERE inactive = 0 AND suspended = 0 AND not_found = 0 ORDER BY name")
}

func (s *Storage) UpdateInactiveStatus(max_age time.Duration) error {
	// We use two SQL statements instead of one because SQLite is too limited
	// to do that in a single statement that isn't exceedingly complicated.
	template := `
		UPDATE user_archive SET inactive = ?
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

// Make sure the comments are all from the same user and the user struct is up to date
// This method may seem to have a lot of logic for something in the storage layer,
// but most of it used to be in the scanner for reddit and outside of a transaction;
// putting the data-consistency related logic here simplifies greatly the overall code.
func (s *Storage) SaveCommentsUpdateUser(comments []Comment, user User, maxAge time.Duration) (User, error) {
	if user.Suspended {
		return user, s.SuspendUser(user.Name)
	} else if user.NotFound {
		return user, s.NotFoundUser(user.Name)
	}

	tx := s.db.MustBegin()

	stmt, err := tx.PrepareNamed(`
		INSERT INTO comments VALUES (:id, :author, :score, :permalink, :sub, :created, :body)
		ON CONFLICT(id) DO UPDATE SET
			score=excluded.score,
			body=excluded.body
	`)
	if err != nil {
		tx.Rollback()
		return user, err
	}
	defer stmt.Close()

	for _, comment := range comments {
		stmt.MustExec(comment)
	}

	// Frow now on we don't need to check for an error because if the user doesn't exist,
	// then the constraints would have made the previous statement fail.

	// Use time.Time.Round to remove the monotonic clock measurement, as
	// we don't need it for the precision we want and one parameter depends
	// on an external source (the comments' timestamps).
	now := time.Now().Round(0)
	user.BatchSize = 0
	for _, comment := range comments {
		if now.Sub(comment.CreatedTime()) < maxAge {
			user.BatchSize++
		}
	}

	if user.New && user.Position == "" { // end of the listing reached
		tx.MustExec("UPDATE user_archive SET new = 0 WHERE name = ?", user.Name)
	}

	if !user.New && user.BatchSize < uint(len(comments)) { // position resetting doesn't apply to new users
		user.Position = ""
	}

	if user.BatchSize == uint(len(comments)) {
		user.BatchSize = MaxRedditListingLength
	}

	tx.MustExec("UPDATE user_archive SET position = ?, batch_size = ? WHERE name = ?", user.Position, user.BatchSize, user.Name)

	return user, tx.Commit()
}

func (s *Storage) GetCommentsBelowBetween(score int64, since, until time.Time) []Comment {
	q := `SELECT
			comments.id, comments.author, comments.score, comments.sub,
			comments.permalink, comments.body, comments.created
		FROM users JOIN comments
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

func (s *Storage) GetPositiveKarma(username string) (int64, error) {
	return s.getKarma("SELECT SUM(score) FROM comments WHERE score > 0 AND author = ? COLLATE NOCASE", username)
}

func (s *Storage) GetNegativeKarma(username string) (int64, error) {
	return s.getKarma("SELECT SUM(score) FROM comments WHERE score < 0 AND author = ? COLLATE NOCASE", username)
}

func (s *Storage) getKarma(q, username string) (int64, error) {
	var score sql.NullInt64
	err := s.db.Get(&score, q, username)
	if err != nil {
		return 0, err
	} else if !score.Valid {
		return 0, ErrNoComment
	}
	return score.Int64, nil
}

func (s *Storage) StatsBetween(since, until time.Time) UserStatsMap {
	stmt, err := s.db.Prepare(`
		SELECT
			comments.author AS author,
			AVG(comments.score) AS average,
			SUM(comments.score) AS delta,
			COUNT(comments.id) AS count
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE
			comments.score < 0
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

func (s *Storage) SaveSubPostIDs(sub string, listing []Comment) error {
	var err error
	// We are limited to one connection to the database, and readSubPostIDs may use one,
	// so we have to wrap everything into it to avoid a deadlock.
	s.readSubPostIDs(sub, func(ids *SyncSet) {
		tx := s.db.MustBegin()
		stmt, err := tx.Preparex("INSERT INTO seen_posts(id, sub, created) VALUES (?, ?, ?)")
		if err != nil {
			tx.Rollback()
			return
		}
		defer stmt.Close()

		ids.Transaction(func(data map[string]bool) {
			for _, post := range listing {
				if _, ok := data[post.Id]; !ok {
					stmt.MustExec(post.Id, sub, post.Created)
					data[post.Id] = true
				}
			}
		})
		err = tx.Commit()
	})

	return err
}

func (s *Storage) IsKnownSubPostID(sub, id string) bool {
	var is_known bool
	s.readSubPostIDs(sub, func(ids *SyncSet) {
		is_known = ids.Has(id)
	})
	return is_known
}

func (s *Storage) NbKnownPostIDs(sub string) int {
	var length int
	s.readSubPostIDs(sub, func(ids *SyncSet) {
		length = ids.Len()
	})
	return length
}

func (s *Storage) readSubPostIDs(sub string, cb func(*SyncSet)) {
	s.cache.SubPostIDsLock.Lock()
	defer s.cache.SubPostIDsLock.Unlock()

	cache := s.cache.SubPostIDs

	if _, ok := cache[sub]; !ok {
		cache[sub] = NewSyncSet()
		cache[sub].MultiPut(s.getPostIDsOf(sub))
	}

	cb(cache[sub])
}

func (s *Storage) getPostIDsOf(sub string) []string {
	var ids []string
	err := s.db.Select(&ids, "SELECT id FROM seen_posts WHERE sub = ?", sub)
	if err != nil && err != sql.ErrNoRows {
		panic(err)
	}
	return ids
}

func (s *Storage) SaveKnownObject(id string) error {
	_, err := s.db.Exec("INSERT INTO known_objects VALUES (?, strftime(\"%s\", CURRENT_TIMESTAMP))", id)
	if err != nil {
		return err
	}
	s.cache.KnownObjects.Put(id)
	return nil
}

func (s *Storage) IsKnownObject(id string) bool {
	return s.cache.KnownObjects.Has(id)
}

func (s *Storage) getKnownObjects() ([]string, error) {
	var ids []string
	err := s.db.Select(&ids, "SELECT id FROM known_objects")
	if err == sql.ErrNoRows {
		err = nil
	}
	return ids, err
}
