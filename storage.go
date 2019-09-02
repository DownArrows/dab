package main

import (
	"context"
	"fmt"
	"time"
)

// ApplicationFileID is the identification integer written in the SQLite file specific to the application.
const ApplicationFileID int = 0xdab

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
		Retry:           conf.Retry,
		Timeout:         conf.Timeout.Value,
		Version:         Version,
	})
	if err != nil {
		return nil, err
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	kv, err := NewKeyValueStore(conn, "key_value")
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

	if err := s.initTables(conn); err != nil {
		return nil, err
	}

	return s, nil
}

// GetConn creates new connections to the associated database.
func (s *Storage) GetConn(ctx context.Context) (*SQLiteConn, error) {
	return s.db.GetConn(ctx)
}

func (s *Storage) initTables(conn *SQLiteConn) error {
	var queries []SQLQuery
	queries = append(queries, User{}.InitializationQueries()...)
	queries = append(queries, Comment{}.InitializationQueries()...)
	if err := conn.MultiExec(queries); err != nil {
		return err
	}
	return nil
}

// KV return a key-value store.
func (s *Storage) KV() *KeyValueStore {
	return s.kv
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
func (s *Storage) Backup(ctx context.Context, conn *SQLiteConn) error {
	if older, err := FileOlderThan(s.BackupPath(), s.backupMaxAge); err != nil {
		return err
	} else if !older {
		s.logger.Debugf("database backup was not older than %v, nothing was done", s.backupMaxAge)
		return nil
	}
	return s.db.Backup(ctx, conn, SQLiteBackupOptions{
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
func (s *Storage) GetUser(conn *SQLiteConn, username string) UserQuery {
	user := &User{}
	cb := func(stmt *SQLiteStmt) error { return user.FromDB(stmt) }
	err := conn.Select("SELECT * FROM users WHERE name = ? COLLATE NOCASE", cb, username)
	return UserQuery{
		Error:  err,
		Exists: user.Name != "",
		User:   *user,
	}
}

// ListUsers lists all users that are neither suspended nor deleted,
// ordered from the least recently scanned.
func (s *Storage) ListUsers(conn *SQLiteConn) ([]User, error) {
	return s.users(conn, "SELECT * FROM users WHERE suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

// ListSuspendedAndNotFound lists users that are either deleted or suspended, but not those that have been unregistered,
// ordered from the least recently scanned.
func (s *Storage) ListSuspendedAndNotFound(conn *SQLiteConn) ([]User, error) {
	return s.users(conn, "SELECT * FROM users WHERE suspended IS TRUE OR not_found IS TRUE ORDER BY last_scan")
}

// ListActiveUsers returns users that are considered active by the application,
// ordered from the least recently scanned.
func (s *Storage) ListActiveUsers(conn *SQLiteConn) ([]User, error) {
	return s.users(conn, "SELECT * FROM users WHERE inactive IS FALSE AND suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

// ListRegisteredUsers returns all registered users, even if deleted or suspended, ordered from the most recently scanned.
func (s *Storage) ListRegisteredUsers(conn *SQLiteConn) ([]User, error) {
	return s.users(conn, "SELECT * FROM users ORDER BY last_scan DESC")
}

func (s *Storage) users(conn *SQLiteConn, sql string) ([]User, error) {
	var users []User
	err := conn.Select(sql, func(stmt *SQLiteStmt) error {
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
func (s *Storage) UpdateInactiveStatus(conn *SQLiteConn, maxAge time.Duration) error {
	sql := `
		WITH data AS (
			SELECT author FROM comments
			GROUP BY author HAVING (? - MAX(created)) > ?
		)
		UPDATE user_archive SET inactive = (name IN data)`
	return conn.Exec(sql, time.Now().Unix(), maxAge.Seconds())
}

// AddUser adds a User to the database. It doesn't check with Reddit, that is the responsibility of RedditUsers.
func (s *Storage) AddUser(conn *SQLiteConn, username string, hidden bool, created time.Time) error {
	sql := "INSERT INTO user_archive(name, hidden, created, added) VALUES (?, ?, ?, ?)"
	return conn.Exec(sql, username, hidden, created.Unix(), time.Now().Unix())
}

// DelUser deletes a User that has the case-insensitive username.
func (s *Storage) DelUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET deleted = TRUE WHERE name = ? COLLATE NOCASE", username)
}

// UnDelUser undeletes a User that has the case-insensitive username.
func (s *Storage) UnDelUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET deleted = FALSE WHERE name = ? COLLATE NOCASE", username)
}

// HideUser hides a User that has the case-insensitive username.
func (s *Storage) HideUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET hidden = TRUE WHERE name = ? COLLATE NOCASE", username)
}

// UnHideUser un-hides a User that has the case-insensitive username.
func (s *Storage) UnHideUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET hidden = FALSE WHERE name = ? COLLATE NOCASE", username)
}

// SuspendUser sets a User as suspended (case-sensitive).
func (s *Storage) SuspendUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET suspended = TRUE WHERE name = ?", username)
}

// UnSuspendUser unsets a User as suspended (case-sensitive).
func (s *Storage) UnSuspendUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET suspended = FALSE WHERE name = ?", username)
}

// NotFoundUser sets a User as not found (case-sensitive), that is, seen from Reddit as deleted.
func (s *Storage) NotFoundUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET not_found = TRUE WHERE name = ?", username)
}

// FoundUser sets a User as found on Reddit (case-sensitive).
func (s *Storage) FoundUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "UPDATE user_archive SET not_found = FALSE WHERE name = ?", username)
}

// PurgeUser completely removes the data associated with a User (case-insensitive).
func (s *Storage) PurgeUser(conn *SQLiteConn, username string) error {
	return s.simpleEditUser(conn, "DELETE FROM user_archive WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) simpleEditUser(conn *SQLiteConn, sql, username string) error {
	if err := conn.Exec(sql, username); err != nil {
		return err
	}
	if conn.Changes() == 0 {
		return fmt.Errorf("no user named %q", username)
	}
	return nil
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
func (s *Storage) SaveCommentsUpdateUser(conn *SQLiteConn, comments []Comment, user User, maxAge time.Duration) (User, error) {
	if user.Suspended {
		return user, s.SuspendUser(conn, user.Name)
	} else if user.NotFound {
		return user, s.NotFoundUser(conn, user.Name)
	}

	err := conn.WithTx(func() error {
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
	cb := func(stmt *SQLiteStmt) error {
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
func (s *Storage) GetKarma(conn *SQLiteConn, username string) (int64, int64, error) {
	sql := `
		SELECT SUM(score), SUM(CASE WHEN score < 0 THEN score ELSE NULL END)
		FROM comments WHERE author = ? COLLATE NOCASE`
	var total int64
	var negative int64
	var err error
	cb := func(stmt *SQLiteStmt) error {
		// In both cases assume 0 if NULL, as it's not really
		// useful to gracefully handle this corner case.
		total, _, err = stmt.ColumnInt64(0)
		if err != nil {
			return err
		}
		negative, _, err = stmt.ColumnInt64(1)
		return err
	}
	err = conn.Select(sql, cb, username)
	return total, negative, err
}

// StatsBetween returns the commenting statistics of all non-hidden users below a score, between since and until.
// To be used within a transaction.
func (s *Storage) StatsBetween(conn *SQLiteConn, score int64, since, until time.Time) (StatsCollection, error) {
	return s.selectStats(conn, StatsRead{Name: true}, `
		SELECT
			COUNT(comments.id),
			SUM(comments.score) AS total,
			AVG(comments.score),
			comments.author
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE
			comments.score < 0
			AND users.hidden IS FALSE
			AND comments.created BETWEEN ? AND ?
		GROUP BY comments.author
		HAVING MIN(comments.score) <= ?
		ORDER BY total`, since.Unix(), until.Unix(), score)
}

// CompendiumPerUser returns the per-user statistics of all users, for use with the compendium.
func (s *Storage) CompendiumPerUser(conn *SQLiteConn) (StatsCollection, StatsCollection, error) {
	return s.compendiumSelectStats(conn, `
		SELECT
		/* All comments */
			COUNT(comments.id),
			SUM(comments.score) AS karma,
			AVG(comments.score),
			comments.author,
			MAX(comments.created),
		/* Only negative comments */
			COUNT(CASE WHEN comments.score < 0 THEN 1 ELSE NULL END),
			SUM(CASE WHEN comments.score < 0 THEN comments.score ELSE NULL END),
			AVG(CASE WHEN comments.score < 0 THEN comments.score ELSE NULL END),
			MAX(CASE WHEN comments.score < 0 THEN comments.created ELSE NULL END)
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE users.hidden IS FALSE
		GROUP BY author
		ORDER BY karma ASC`)
}

// CompendiumUserPerSub returns the commenting statistics for a single user, for use with the compendium.
func (s *Storage) CompendiumUserPerSub(conn *SQLiteConn, username string) (StatsCollection, StatsCollection, error) {
	return s.compendiumSelectStats(conn, `
		SELECT
		/* All comments */
			COUNT(score), SUM(score) AS karma, AVG(score), sub, MAX(created),
		/* Only negative comments */
			COUNT(CASE WHEN score < 0 THEN 1 ELSE NULL END),
			SUM(CASE WHEN score < 0 THEN score ELSE NULL END),
			AVG(CASE WHEN score < 0 THEN score ELSE NULL END),
			MAX(CASE WHEN score < 0 THEN created ELSE NULL END)
		FROM comments WHERE author = ?
		GROUP BY sub
		ORDER BY karma ASC`, username)
}

func (s *Storage) compendiumSelectStats(conn *SQLiteConn, sql string, args ...interface{}) (StatsCollection, StatsCollection, error) {
	var all StatsCollection
	var negative StatsCollection

	allRead := StatsRead{Name: true, Latest: true}
	negRead := StatsRead{Start: 5, Latest: true}

	cb := func(stmt *SQLiteStmt) error {
		allStats := &Stats{}
		negStats := &Stats{}
		if err := allStats.FromDB(stmt, allRead); err != nil {
			return err
		}
		all = append(all, *allStats)

		if err := negStats.FromDB(stmt, negRead); err != nil {
			return err
		}
		negStats.Name = allStats.Name
		negative = append(negative, *negStats)

		return nil
	}

	err := conn.Select(sql, cb, args...)
	return all, negative, err
}

func (s *Storage) selectStats(conn *SQLiteConn, statsRead StatsRead, sql string, args ...interface{}) (StatsCollection, error) {
	var data StatsCollection
	cb := func(stmt *SQLiteStmt) error {
		stats := &Stats{}
		if err := stats.FromDB(stmt, statsRead); err != nil {
			return err
		}
		data = append(data, *stats)
		return nil
	}
	err := conn.Select(sql, cb, args...)
	return data, err
}
