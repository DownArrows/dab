package main

import (
	"context"
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"time"
)

// ApplicationFileID is the identification integer written in the SQLite file specific to the application.
const ApplicationFileID int = 0xdab

// RedditScannerStorage is the storage interface for RedditScanner.
type RedditScannerStorage interface {
	KV() *KeyValueStore
	ListActiveUsers(context.Context) ([]User, error)
	ListSuspendedAndNotFound(context.Context) ([]User, error)
	ListUsers(context.Context) ([]User, error)
	SaveCommentsUpdateUser(context.Context, []Comment, User, time.Duration) (User, error)
	UpdateInactiveStatus(context.Context, time.Duration) error
}

// RedditUsersStorage is the storage interface for RedditUsers.
type RedditUsersStorage interface {
	AddUser(context.Context, string, bool, time.Time) error
	FoundUser(context.Context, string) error
	GetUser(context.Context, string) UserQuery
	ListSuspendedAndNotFound(context.Context) ([]User, error)
	NotFoundUser(context.Context, string) error
	SuspendUser(context.Context, string) error
	UnSuspendUser(context.Context, string) error
}

// DiscordBotStorage is the storage interface for RedditUsers.
type DiscordBotStorage interface {
	DelUser(context.Context, string) error
	PurgeUser(context.Context, string) error
	HideUser(context.Context, string) error
	UnHideUser(context.Context, string) error
	GetUser(context.Context, string) UserQuery
	GetKarma(context.Context, string) (int64, int64, error)
}

// ReportFactoryStorage is the storage interface for ReportFactory.
type ReportFactoryStorage interface {
	GetCommentsBelowBetween(*SQLiteConn, int64, time.Time, time.Time) ([]Comment, error)
	StatsBetween(*SQLiteConn, int64, time.Time, time.Time) (UserStatsMap, error)
	WithTx(context.Context, func(*SQLiteConn) error) error
}

// WebServerStorage is the storage interface for the WebServer.
type WebServerStorage interface {
	BackupStorage
	GetUser(context.Context, string) UserQuery
}

// CompendiumStorage is the storage interface for the Compendium.
type CompendiumStorage interface {
	// Index
	CompendiumPerUser(*SQLiteConn) ([]*CompendiumDetailsTagged, error)
	CompendiumPerUserNegative(*SQLiteConn) ([]*CompendiumDetailsTagged, error)
	ListRegisteredUsers(*SQLiteConn) ([]User, error)
	TopComments(*SQLiteConn, uint) ([]Comment, error)
	// User pages
	CompendiumUserPerSub(*SQLiteConn, string) ([]*CompendiumDetailsTagged, error)
	CompendiumUserPerSubNegative(*SQLiteConn, string) ([]*CompendiumDetailsTagged, error)
	CompendiumUserSummary(*SQLiteConn, string) (*CompendiumDetails, error)
	CompendiumUserSummaryNegative(*SQLiteConn, string) (*CompendiumDetails, error)
	TopCommentsUser(*SQLiteConn, string, uint) ([]Comment, error)
	// Other
	WithTx(context.Context, func(*SQLiteConn) error) error
}

// BackupStorage is the interface for backups.
type BackupStorage interface {
	Backup(context.Context) error
	BackupPath() string
}

// Storage is a collection of methods to write, update, and retrieve all persistent data used throughout the application.
type Storage struct {
	backupPath   string
	backupMaxAge time.Duration
	db           *SQLiteDatabase
	kv           *KeyValueStore
	logger       LevelLogger
}

// NewStorage returns a Storage instance after running initialization, checks, and migrations onto the target database file.
func NewStorage(ctx context.Context, logger LevelLogger, conf StorageConf) (*Storage, error) {
	db, err := NewSQLiteDatabase(ctx, logger, SQLiteDatabaseOptions{
		AppID:           ApplicationFileID,
		CleanupInterval: conf.CleanupInterval.Value,
		Migrations:      StorageMigrations,
		Path:            conf.Path,
		Timeout:         conf.Timeout.Value,
		Version:         Version,
	})
	if err != nil {
		return nil, err
	}

	kv, err := NewKeyValueStore(ctx, db, "key_value")
	if err != nil {
		return nil, err
	}

	s := &Storage{
		backupMaxAge: conf.BackupMaxAge.Value,
		backupPath:   conf.BackupPath,
		db:           db,
		kv:           kv,
		logger:       logger,
	}

	if err := s.initTables(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Storage) initTables(ctx context.Context) error {
	var queries []SQLQuery
	queries = append(queries, User{}.InitializationQueries()...)
	queries = append(queries, Comment{}.InitializationQueries()...)
	if err := s.db.MultiExec(ctx, queries); err != nil {
		return err
	}
	return nil
}

// KV return a key-value store.
func (s *Storage) KV() *KeyValueStore {
	return s.kv
}

// WithTx is a wrapper for SQLiteDatabase.WithTx.
func (s *Storage) WithTx(ctx context.Context, cb func(*SQLiteConn) error) error {
	return s.db.WithTx(ctx, cb)
}

// WithConn runs a callback with a connection to the database, managing its lifecycle.
func (s *Storage) WithConn(ctx context.Context, cb func(*SQLiteConn) error) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return cb(conn)
}

/***********
Maintenance
***********/

// PeriodicCleanupIsEnabled tells if the setting for PeriodCleanup allow to run it.
func (s *Storage) PeriodicCleanupIsEnabled() bool {
	return s.db.CleanupInterval > 0
}

// PeriodicCleanup is a Task that periodically cleans up and optimizes the underlying database.
func (s *Storage) PeriodicCleanup(ctx context.Context) error {
	return s.db.PeriodicCleanup(ctx)
}

// BackupPath returns the set path for backups.
func (s *Storage) BackupPath() string {
	return s.backupPath
}

// Backup performs a backup on the destination returned by BackupPath.
func (s *Storage) Backup(ctx context.Context) error {
	if older, err := FileOlderThan(s.BackupPath(), s.backupMaxAge); err != nil {
		return err
	} else if !older {
		s.logger.Debugf("database backup was not older than %v, nothing was done", s.backupMaxAge)
		return nil
	}
	return s.db.Backup(ctx, SQLiteBackupOptions{
		DestName: "main",
		DestPath: s.BackupPath(),
		SrcName:  "main",
	})
}

/*****
Users
******/

// Read

// GetUser fetches a User, within a UserQuery for easier use, from a case-insensitive name.
func (s *Storage) GetUser(ctx context.Context, username string) UserQuery {
	user := &User{}
	cb := func(stmt *sqlite.Stmt) error { return user.FromDB(stmt) }
	err := s.db.Select(ctx, "SELECT * FROM users WHERE name = ? COLLATE NOCASE", cb, username)
	return UserQuery{
		Error:  err,
		Exists: user.Name != "",
		User:   *user,
	}
}

// ListUsers lists all users that are neither suspended nor deleted,
// ordered from the least recently scanned.
func (s *Storage) ListUsers(ctx context.Context) ([]User, error) {
	return s.usersCtx(ctx, "SELECT * FROM users WHERE suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

// ListSuspendedAndNotFound lists users that are either deleted or suspended, but not those that have been unregistered,
// ordered from the least recently scanned.
func (s *Storage) ListSuspendedAndNotFound(ctx context.Context) ([]User, error) {
	return s.usersCtx(ctx, "SELECT * FROM users WHERE suspended IS TRUE OR not_found IS TRUE ORDER BY last_scan")
}

// ListActiveUsers returns users that are considered active by the application,
// ordered from the least recently scanned.
func (s *Storage) ListActiveUsers(ctx context.Context) ([]User, error) {
	return s.usersCtx(ctx, "SELECT * FROM users WHERE inactive IS FALSE AND suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

// ListRegisteredUsers returns all registered users, even if deleted or suspended, ordered from the most recently scanned.
func (s *Storage) ListRegisteredUsers(conn *SQLiteConn) ([]User, error) {
	return s.users(conn, "SELECT * FROM users ORDER BY last_scan DESC")
}

func (s *Storage) usersCtx(ctx context.Context, sql string) ([]User, error) {
	var users []User
	var err error
	err = s.WithConn(ctx, func(conn *SQLiteConn) error {
		users, err = s.users(conn, sql)
		return err
	})
	return users, err
}

func (s *Storage) users(conn *SQLiteConn, sql string) ([]User, error) {
	var users []User
	err := conn.Select(sql, func(stmt *sqlite.Stmt) error {
		user := &User{}
		if err := user.FromDB(stmt); err != nil {
			return err
		}
		users = append(users, *user)
		return nil
	})
	return users, err
}

// Write

// UpdateInactiveStatus updates what is considered for a user to be "inactive",
// that is, if they haven't posted since maxAge.
func (s *Storage) UpdateInactiveStatus(ctx context.Context, maxAge time.Duration) error {
	sql := `
		WITH data AS (
			SELECT author FROM comments
			GROUP BY author HAVING (? - MAX(created)) > ?
		)
		UPDATE user_archive SET inactive = (name IN data)`
	return s.db.Exec(ctx, sql, time.Now().Unix(), maxAge.Seconds())
}

// AddUser adds a User to the database. It doesn't check with Reddit, that is the responsibility of RedditUsers.
func (s *Storage) AddUser(ctx context.Context, username string, hidden bool, created time.Time) error {
	sql := "INSERT INTO user_archive(name, hidden, created, added) VALUES (?, ?, ?, ?)"
	return s.db.Exec(ctx, sql, username, hidden, created.Unix(), time.Now().Unix())
}

// DelUser deletes a User that has the case-insensitive username.
func (s *Storage) DelUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET deleted = TRUE WHERE name = ? COLLATE NOCASE", username)
}

// HideUser hides a User that has the case-insensitive username.
func (s *Storage) HideUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET hidden = TRUE WHERE name = ? COLLATE NOCASE", username)
}

// UnHideUser un-hides a User that has the case-insensitive username.
func (s *Storage) UnHideUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET hidden = FALSE WHERE name = ? COLLATE NOCASE", username)
}

// SuspendUser sets a User as suspended (case-sensitive).
func (s *Storage) SuspendUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET suspended = TRUE WHERE name = ?", username)
}

// UnSuspendUser unsets a User as suspended (case-sensitive).
func (s *Storage) UnSuspendUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET suspended = FALSE WHERE name = ?", username)
}

// NotFoundUser sets a User as not found (case-sensitive), that is, seen from Reddit as deleted.
func (s *Storage) NotFoundUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET not_found = TRUE WHERE name = ?", username)
}

// FoundUser sets a User as found on Reddit (case-sensitive).
func (s *Storage) FoundUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET not_found = FALSE WHERE name = ?", username)
}

// PurgeUser completely removes the data associated with a User (case-insensitive).
func (s *Storage) PurgeUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "DELETE FROM user_archive WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) simpleEditUser(ctx context.Context, sql, username string) error {
	return s.WithConn(ctx, func(conn *SQLiteConn) error {
		if err := conn.Exec(sql, username); err != nil {
			return err
		}
		if conn.TotalChanges() == 0 {
			return fmt.Errorf("no user named %q", username)
		}
		return nil
	})
}

/********
 Comments
*********/

// SaveCommentsUpdateUser saves comments of a single user, and updates the user's metadata according
// to the properties of the list of comments. It returns the updated User data structure to
// avoid having to do another request to get the update.
// What happens here controls how the scanner will behave next.
//
// Make sure the comments are all from the same user and the user struct is up to date.
// This method may seem to have a lot of logic for something in the storage layer,
// but most of it used to be in the scanner for reddit and outside of a transaction;
// putting the data-consistency related logic here simplifies greatly the overall code.
func (s *Storage) SaveCommentsUpdateUser(ctx context.Context, comments []Comment, user User, maxAge time.Duration) (User, error) {
	if user.Suspended {
		return user, s.SuspendUser(ctx, user.Name)
	} else if user.NotFound {
		return user, s.NotFoundUser(ctx, user.Name)
	}

	err := s.db.WithTx(ctx, func(conn *SQLiteConn) error {
		stmt, err := conn.Prepare(`
			INSERT INTO comments VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				score=excluded.score,
				body=excluded.body`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, comment := range comments {
			if err := stmt.Exec(comment.ToDB()...); err != nil {
				return err
			}
			if err := stmt.ClearBindings(); err != nil {
				return err
			}
		}

		// Frow now on we don't need to check for an error because if the user doesn't exist,
		// then the constraints would have made the previous statement fail.

		// We need to know how many relevant we got, and save it in the user's metadata.
		// This way, the scanner can avoid fetching superfluous comments.
		user.BatchSize = 0
		for _, comment := range comments {
			if time.Now().Sub(comment.Created) < maxAge {
				user.BatchSize++
			}
		}

		if user.New && user.Position == "" { // end of the listing reached
			if err := conn.Exec("UPDATE user_archive SET new = FALSE WHERE name = ?", user.Name); err != nil {
				return err
			}
		}

		if !user.New && user.BatchSize < uint(len(comments)) { // position resetting doesn't apply to new users
			user.Position = ""
		}

		// All comments are younger than maxAge, there may be more.
		if user.BatchSize == uint(len(comments)) {
			user.BatchSize = MaxRedditListingLength
		}

		user.LastScan = time.Now()

		sql := "UPDATE user_archive SET position = ?, batch_size = ?, last_scan = ? WHERE name = ?"
		return conn.Exec(sql, user.Position, int(user.BatchSize), user.LastScan.Unix(), user.Name)
	})

	return user, err
}

// GetCommentsBelowBetween returns the comments below a score, between since and until.
// To be used within a transaction.
func (s *Storage) GetCommentsBelowBetween(conn *SQLiteConn, score int64, since, until time.Time) ([]Comment, error) {
	return s.comments(conn, `
			SELECT comments.*
			FROM users JOIN comments
			ON comments.author = users.name
			WHERE
				comments.score <= ?
				AND users.hidden IS FALSE
				AND comments.created BETWEEN ? AND ?
			ORDER BY comments.score ASC
		`, score, since.Unix(), until.Unix())
}

// TopComments returns the most downvoted comments, up to a number set by limit.
// To be used within a transaction.
func (s *Storage) TopComments(conn *SQLiteConn, limit uint) ([]Comment, error) {
	return s.comments(conn, `
			SELECT comments.*
			FROM users JOIN comments
			ON comments.author = users.name
			WHERE
				comments.score < 0
				AND users.hidden IS FALSE
			ORDER BY score ASC LIMIT ?
		`, int(limit))
}

// TopCommentsUser returns the most downvoted comments of a single User, up to a number set by limit.
// To be used within a transaction.
func (s *Storage) TopCommentsUser(conn *SQLiteConn, username string, limit uint) ([]Comment, error) {
	return s.comments(conn, "SELECT * FROM comments WHERE author = ? AND score < 0 ORDER BY score ASC LIMIT ?", username, int(limit))
}

func (s *Storage) comments(conn *SQLiteConn, sql string, args ...interface{}) ([]Comment, error) {
	var comments []Comment
	cb := func(stmt *sqlite.Stmt) error {
		comment := &Comment{}
		if err := comment.FromDB(stmt); err != nil {
			return err
		}
		comments = append(comments, *comment)
		return nil
	}
	err := conn.Select(sql, cb, args...)
	return comments, err
}

/**********
 Statistics
***********/

// GetKarma returns the total and negative karma of a User (case-insensitive).
func (s *Storage) GetKarma(ctx context.Context, username string) (int64, int64, error) {
	sql := `
		SELECT SUM(score), SUM(CASE WHEN score < 0 THEN score ELSE NULL END)
		FROM comments WHERE author = ? COLLATE NOCASE`
	var total int64
	var negative int64
	var err error
	cb := func(stmt *sqlite.Stmt) error {
		// In both cases assume 0 if NULL, as it's not really
		// useful to gracefully handle this corner case.
		total, _, err = stmt.ColumnInt64(0)
		if err != nil {
			return err
		}
		negative, _, err = stmt.ColumnInt64(1)
		return err
	}
	err = s.db.Select(ctx, sql, cb, username)
	return total, negative, err
}

// StatsBetween returns the commenting statistics of all non-hidden users below a score, between since and until.
// To be used within a transaction.
func (s *Storage) StatsBetween(conn *SQLiteConn, score int64, since, until time.Time) (UserStatsMap, error) {
	sql := `SELECT
			comments.author AS author,
			AVG(comments.score) AS average,
			SUM(comments.score) AS delta,
			COUNT(comments.id) AS count
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE
			comments.score < 0
			AND users.hidden IS FALSE
			AND comments.created BETWEEN ? AND ?
		GROUP BY comments.author
		HAVING MIN(comments.score) <= ?`

	stats := UserStatsMap{}
	cb := func(stmt *sqlite.Stmt) error {
		data := &UserStats{}
		if err := data.FromDB(stmt); err != nil {
			return err
		}
		stats[data.Name] = *data
		return nil
	}

	err := conn.Select(sql, cb, since.Unix(), until.Unix(), score)
	return stats, err
}

// CompendiumPerUser returns the per-user statistics of all users, for use with the compendium.
// To be used within a transaction.
func (s *Storage) CompendiumPerUser(conn *SQLiteConn) ([]*CompendiumDetailsTagged, error) {
	return s.compendiumDetailsTagged(conn, `
		SELECT
			COUNT(comments.id),
			AVG(comments.score),
			SUM(comments.score) AS karma,
			MAX(comments.created),
			comments.author
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE users.hidden IS FALSE
		GROUP BY author
		ORDER BY karma ASC`)
}

// CompendiumPerUserNegative returns the per-user statistics of all users taking only into account negative comments.
// For use with the compendium. To be used within a transaction.
func (s *Storage) CompendiumPerUserNegative(conn *SQLiteConn) ([]*CompendiumDetailsTagged, error) {
	return s.compendiumDetailsTagged(conn, `
		SELECT
			COUNT(comments.id),
			AVG(comments.score),
			SUM(comments.score) AS karma,
			MAX(comments.created),
			comments.author
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE
			users.hidden IS FALSE
			AND comments.score < 0
		GROUP BY author
		ORDER BY karma ASC`)
}

// CompendiumUserPerSub returns the commenting statistics for a single user, for use with the compendium.
// To be used within a transaction.
func (s *Storage) CompendiumUserPerSub(conn *SQLiteConn, username string) ([]*CompendiumDetailsTagged, error) {
	return s.compendiumDetailsTagged(conn, `
		SELECT
			COUNT(score), AVG(score), SUM(score) AS karma, MAX(created), sub
		FROM comments WHERE author = ?
		GROUP BY sub
		ORDER BY karma ASC`, username)
}

// CompendiumUserPerSubNegative returns the commenting statistics for a single user and negative comments only, for use with the compendium.
// To be used within a transaction.
func (s *Storage) CompendiumUserPerSubNegative(conn *SQLiteConn, username string) ([]*CompendiumDetailsTagged, error) {
	return s.compendiumDetailsTagged(conn, `
		SELECT
			COUNT(score), AVG(score), SUM(score) AS karma, MAX(created), sub
		FROM comments WHERE author = ? AND score < 0
		GROUP BY sub
		ORDER BY karma ASC`, username)
}

// CompendiumUserSummary returns the summary of the commenting statistics for a single user, for use with the compendium.
// To be used within a transaction.
func (s *Storage) CompendiumUserSummary(conn *SQLiteConn, username string) (*CompendiumDetails, error) {
	sql := "SELECT COUNT(score), AVG(score), SUM(score), MAX(created) FROM comments WHERE author = ?"
	stats := &CompendiumDetails{}
	err := conn.Select(sql, stats.FromDB, username)
	return stats, err
}

// CompendiumUserSummaryNegative returns the summary of the commenting statistics for a single user, for use with the compendium.
// Negative comments only. To be used within a transaction.
func (s *Storage) CompendiumUserSummaryNegative(conn *SQLiteConn, username string) (*CompendiumDetails, error) {
	sql := "SELECT COUNT(score), AVG(score), SUM(score), MAX(created) FROM comments WHERE author = ? AND score < 0"
	stats := &CompendiumDetails{}
	err := conn.Select(sql, stats.FromDB, username)
	return stats, err
}

func (s *Storage) compendiumDetailsTagged(conn *SQLiteConn, sql string, args ...interface{}) ([]*CompendiumDetailsTagged, error) {
	var stats []*CompendiumDetailsTagged
	cb := func(stmt *sqlite.Stmt) error {
		detail := &CompendiumDetailsTagged{}
		if err := detail.FromDB(stmt); err != nil {
			return err
		}
		stats = append(stats, detail)
		return nil
	}
	err := conn.Select(sql, cb, args...)
	return stats, err
}
