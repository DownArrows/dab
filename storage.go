package main

import (
	"context"
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"time"
)

const ApplicationFileID int = 0xdab

type RedditScannerStorage interface {
	KV() *KeyValueStore
	ListActiveUsers(context.Context) ([]User, error)
	ListSuspendedAndNotFound(context.Context) ([]User, error)
	ListUsers(context.Context) ([]User, error)
	SaveCommentsUpdateUser(context.Context, []Comment, User, time.Duration) (User, error)
	UpdateInactiveStatus(context.Context, time.Duration) error
}

type RedditUsersStorage interface {
	AddUser(context.Context, string, bool, time.Time) error
	FoundUser(context.Context, string) error
	GetUser(context.Context, string) UserQuery
	ListSuspendedAndNotFound(context.Context) ([]User, error)
	NotFoundUser(context.Context, string) error
	SuspendUser(context.Context, string) error
	UnSuspendUser(context.Context, string) error
}

type DiscordBotStorage interface {
	DelUser(context.Context, string) error
	PurgeUser(context.Context, string) error
	HideUser(context.Context, string) error
	UnHideUser(context.Context, string) error
	GetUser(context.Context, string) UserQuery
	GetKarma(context.Context, string) (int64, int64, error)
}

type ReportFactoryStorage interface {
	GetCommentsBelowBetween(*SQLiteConn, int64, time.Time, time.Time) ([]Comment, error)
	StatsBetween(*SQLiteConn, int64, time.Time, time.Time) (UserStatsMap, error)
	WithTx(context.Context, func(*SQLiteConn) error) error
}

type WebServerStorage interface {
	BackupStorage
	GetUser(context.Context, string) UserQuery
}

type CompendiumStorage interface {
	CompendiumUserStatsPerSub(*SQLiteConn, string) ([]*CompendiumUserStatsDetailsPerSub, error)
	CompendiumUserStatsPerSubNegative(*SQLiteConn, string) ([]*CompendiumUserStatsDetailsPerSub, error)
	CompendiumUserStatsSummary(*SQLiteConn, string) (*CompendiumUserStatsDetails, error)
	CompendiumUserStatsSummaryNegative(*SQLiteConn, string) (*CompendiumUserStatsDetails, error)
	UserTopComments(*SQLiteConn, string, uint) ([]Comment, error)
	WithTx(context.Context, func(*SQLiteConn) error) error
}

type BackupStorage interface {
	Backup(context.Context) error
	BackupPath() string
}

type Storage struct {
	backupPath   string
	backupMaxAge time.Duration
	db           *SQLiteDatabase
	kv           *KeyValueStore
	logger       LevelLogger
}

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

func (s *Storage) KV() *KeyValueStore {
	return s.kv
}

func (s *Storage) WithTx(ctx context.Context, cb func(*SQLiteConn) error) error {
	return s.db.WithTx(ctx, cb)
}

/***********
Maintenance
***********/

func (s *Storage) PeriodicCleanupIsEnabled() bool {
	return s.db.CleanupInterval > 0
}

func (s *Storage) PeriodicCleanup(ctx context.Context) error {
	return s.db.PeriodicCleanup(ctx)
}

func (s *Storage) BackupPath() string {
	return s.backupPath
}

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

func (s *Storage) ListUsers(ctx context.Context) ([]User, error) {
	return s.anyListUsers(ctx, "SELECT * FROM users WHERE suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

func (s *Storage) ListSuspendedAndNotFound(ctx context.Context) ([]User, error) {
	return s.anyListUsers(ctx, "SELECT * FROM users WHERE suspended IS TRUE OR not_found IS TRUE ORDER BY last_scan")
}

func (s *Storage) ListActiveUsers(ctx context.Context) ([]User, error) {
	return s.anyListUsers(ctx, "SELECT * FROM users WHERE inactive IS FALSE AND suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

func (s *Storage) anyListUsers(ctx context.Context, sql string) ([]User, error) {
	var users []User
	err := s.db.Select(ctx, sql, func(stmt *sqlite.Stmt) error {
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

func (s *Storage) UpdateInactiveStatus(ctx context.Context, max_age time.Duration) error {
	sql := `
		WITH data AS (
			SELECT author FROM comments
			GROUP BY author HAVING (? - MAX(created)) > ?
		)
		UPDATE user_archive SET inactive = (name IN data)`
	return s.db.Exec(ctx, sql, time.Now().Unix(), max_age.Seconds())
}

func (s *Storage) AddUser(ctx context.Context, username string, hidden bool, created time.Time) error {
	sql := "INSERT INTO user_archive(name, hidden, created, added) VALUES (?, ?, ?, ?)"
	return s.db.Exec(ctx, sql, username, hidden, created.Unix(), time.Now().Unix())
}

func (s *Storage) DelUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET deleted = TRUE WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) HideUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET hidden = TRUE WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) UnHideUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET hidden = FALSE WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) SuspendUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET suspended = TRUE WHERE name = ?", username)
}

func (s *Storage) UnSuspendUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET suspended = FALSE WHERE name = ?", username)
}

func (s *Storage) NotFoundUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET not_found = TRUE WHERE name = ?", username)
}

func (s *Storage) FoundUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "UPDATE user_archive SET not_found = FALSE WHERE name = ?", username)
}

func (s *Storage) PurgeUser(ctx context.Context, username string) error {
	return s.simpleEditUser(ctx, "DELETE FROM user_archive WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) simpleEditUser(ctx context.Context, sql, username string) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Exec(sql, username); err != nil {
		return err
	}
	if conn.TotalChanges() == 0 {
		return fmt.Errorf("no user named %q", username)
	}
	return nil
}

/********
 Comments
*********/

// This method saves comments of a single user, and updates the user's metadata according
// to the properties of the list of comments. It returns the updated User datastructure to
// avoid having to do another request to get the update.
// What happens here controls how the scanner will behave next.
//
// Make sure the comments are all from the same user and the user struct is up to date.
// This method may seem to have a lot of logic for something in the storage layer,
// but most of it used to be in the scanner for reddit and outside of a transaction;
// putting the data-consistency related logic here simplifies greatly the overall code.
func (s *Storage) SaveCommentsUpdateUser(ctx context.Context, comments []Comment, user User, max_age time.Duration) (User, error) {
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
			if time.Now().Sub(comment.Created) < max_age {
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

		// All comments are younger than max_age, there may be more.
		if user.BatchSize == uint(len(comments)) {
			user.BatchSize = MaxRedditListingLength
		}

		user.LastScan = time.Now()

		sql := "UPDATE user_archive SET position = ?, batch_size = ?, last_scan = ? WHERE name = ?"
		return conn.Exec(sql, user.Position, int(user.BatchSize), user.LastScan.Unix(), user.Name)
	})

	return user, err
}

func (s *Storage) GetCommentsBelowBetween(conn *SQLiteConn, score int64, since, until time.Time) ([]Comment, error) {
	sql := `SELECT
			comments.*
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE
			comments.score <= ?
			AND users.hidden IS FALSE
			AND comments.created BETWEEN ? AND ?
		ORDER BY comments.score ASC`
	var comments []Comment
	cb := func(stmt *sqlite.Stmt) error {
		comment := &Comment{}
		if err := comment.FromDB(stmt); err != nil {
			return err
		}
		comments = append(comments, *comment)
		return nil
	}
	err := conn.Select(sql, cb, score, since.Unix(), until.Unix())
	return comments, err
}

func (s *Storage) UserTopComments(conn *SQLiteConn, username string, limit uint) ([]Comment, error) {
	sql := "SELECT * FROM comments WHERE author = ? AND score < 0 ORDER BY score ASC LIMIT ?"

	var comments []Comment

	cb := func(stmt *sqlite.Stmt) error {
		comment := &Comment{}
		if err := comment.FromDB(stmt); err != nil {
			return err
		}
		comments = append(comments, *comment)
		return nil
	}

	err := conn.Select(sql, cb, username, int(limit))

	return comments, err
}

/**********
 Statistics
***********/

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

//func (s *Storage) CompendiumStatsSummary(conn *SQLiteConn, username string) (CompendiumStats, error) {
//	// TODO last scan, number of users, number who have posted in the last X hours
//}
//
//func (s *Storage) CompendiumStatsPerUser(conn *SQLiteConn, username string) (CompendiumStats, error) {
//	sql := `
//		SELECT COUNT(id), AVG(score), SUM(score) AS karma, author
//		FROM comments
//		GROUP BY author
//		ORDER BY karma ASC`
//}
//
//func (s *Storage) CompendiumStatsPerUserNegative(conn *SQLiteConn, username string) (CompendiumStats, error) {
//	sql := `
//		SELECT author, COUNT(id), AVG(score), SUM(score) AS karma
//		FROM comments WHERE score < 0
//		GROUP BY author
//		ORDER BY karma ASC`
//}
//
//func (s *Storage) CompendiumStatsTopComments(conn *SQLiteConn, limit uint) (CompendiumStats, error) {
//	sql := "SELECT * FROM comments WHERE score < 0 ORDER BY score ASC LIMIT ?"
//}

func (s *Storage) CompendiumUserStatsPerSub(conn *SQLiteConn, username string) ([]*CompendiumUserStatsDetailsPerSub, error) {
	sql := `
		SELECT
			COUNT(score), AVG(score), SUM(score) AS karma, MAX(created), MIN(created), sub
		FROM comments WHERE author = ?
		GROUP BY sub
		ORDER BY karma ASC`

	var stats []*CompendiumUserStatsDetailsPerSub

	cb := func(stmt *sqlite.Stmt) error {
		detail := &CompendiumUserStatsDetailsPerSub{}
		if err := detail.FromDB(stmt); err != nil {
			return err
		}
		stats = append(stats, detail)
		return nil
	}

	err := conn.Select(sql, cb, username)

	return stats, err
}

func (s *Storage) CompendiumUserStatsPerSubNegative(conn *SQLiteConn, username string) ([]*CompendiumUserStatsDetailsPerSub, error) {
	sql := `
		SELECT
			COUNT(score), AVG(score), SUM(score) AS karma, MAX(created), MIN(created), sub
		FROM comments WHERE author = ? AND score < 0
		GROUP BY sub
		ORDER BY karma ASC`

	var stats []*CompendiumUserStatsDetailsPerSub

	cb := func(stmt *sqlite.Stmt) error {
		detail := &CompendiumUserStatsDetailsPerSub{}
		if err := detail.FromDB(stmt); err != nil {
			return err
		}
		stats = append(stats, detail)
		return nil
	}

	err := conn.Select(sql, cb, username)

	return stats, err
}

func (s *Storage) CompendiumUserStatsSummary(conn *SQLiteConn, username string) (*CompendiumUserStatsDetails, error) {
	sql := "SELECT COUNT(score), AVG(score), SUM(score), MAX(created), MIN(created) FROM comments WHERE author = ?"
	stats := &CompendiumUserStatsDetails{}
	err := conn.Select(sql, stats.FromDB, username)
	return stats, err
}

func (s *Storage) CompendiumUserStatsSummaryNegative(conn *SQLiteConn, username string) (*CompendiumUserStatsDetails, error) {
	sql := "SELECT COUNT(score), AVG(score), SUM(score), MAX(created), MIN(created) FROM comments WHERE author = ? AND score < 0"
	stats := &CompendiumUserStatsDetails{}
	err := conn.Select(sql, stats.FromDB, username)
	return stats, err
}
