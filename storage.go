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
	StatsBetween(int64, time.Time, time.Time) UserStatsMap
}

type BackupStorage interface {
	Backup() error
	BackupPath() string
}

type KnownObjects interface {
	IsKnownObject(string) bool
	SaveKnownObject(string) error
}

const ApplicationFileID int32 = 0x00000dab

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
	"PRAGMA auto_vacuum = 'incremental'",
	fmt.Sprintf("PRAGMA application_id = %d", ApplicationFileID),
	fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS user_archive (
			name TEXT PRIMARY KEY,
			created INTEGER NOT NULL,
			not_found BOOLEAN DEFAULT FALSE NOT NULL,
			suspended BOOLEAN DEFAULT FALSE NOT NULL,
			added INTEGER NOT NULL,
			batch_size INTEGER DEFAULT %d NOT NULL,
			deleted BOOLEAN DEFAULT FALSE NOT NULL,
			hidden BOOLEAN NOT NULL,
			inactive BOOLEAN DEFAULT FALSE NOT NULL,
			last_scan INTEGER DEFAULT FALSE NOT NULL,
			new BOOLEAN DEFAULT TRUE NOT NULL,
			position TEXT DEFAULT "" NOT NULL
		) WITHOUT ROWID`, MaxRedditListingLength),
	`CREATE INDEX IF NOT EXISTS user_archive_idx
			ON user_archive (deleted, last_scan, inactive, suspended, not_found, hidden)`,
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
			users(name, created, not_found, suspended, added, batch_size, hidden, inactive, last_scan, new, position)
		AS
			SELECT name, created, not_found, suspended, added, batch_size, hidden, inactive, last_scan, new, position
			FROM user_archive WHERE deleted IS FALSE`,
}

type foreignKeyCheck struct {
	Table        string        `db:"table"`
	RowID        sql.NullInt64 `db:"rowid"`
	Parent       string        `db:"parent"`
	ForeignKeyID int           `db:"fkid"`
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
	logger          LevelLogger
	path            string
	cache           struct {
		KnownObjects   *SyncSet
		SubPostIDs     map[string]*SyncSet
		SubPostIDsLock sync.Mutex
	}

	PeriodicCleanupEnabled bool
}

func NewStorage(logger LevelLogger, conf StorageConf) (*Storage, error) {
	s := &Storage{
		logger: logger,
		backup: storageBackup{
			Path:   conf.BackupPath,
			MaxAge: conf.BackupMaxAge.Value,
		},
		path:                   conf.Path,
		cleanupInterval:        conf.CleanupInterval.Value,
		PeriodicCleanupEnabled: conf.CleanupInterval.Value > 0,
	}

	var ok bool
	defer func() {
		if !ok {
			if err := s.Close(); err != nil {
				s.logger.Error(err)
			}
		}
	}()

	if err := s.connect(); err != nil {
		return nil, err
	}

	if s.path != ":memory:" {
		if is_new, err := s.isNew(); err != nil {
			return nil, err
		} else if is_new {
			s.logger.Info("using new empty database")
		} else {
			if err := s.checkApplicationID(); err != nil {
				return nil, err
			}

			found_version := s.getVersion()
			if err := s.checkVersion(found_version); err != nil {
				return nil, err
			}
			if err := s.migrate(found_version, storageMigrations); err != nil {
				return nil, err
			}
		}

		if err := s.setVersion(Version); err != nil {
			return nil, err
		}

		if err := s.enableWAL(); err != nil {
			return nil, err
		}

		if err := s.startupChecks(); err != nil {
			return nil, err
		}

	} else {
		s.logger.Debug("skipping checks as we are using an in-memory database")
	}

	if err := s.init(); err != nil {
		return nil, err
	}

	ok = true
	return s, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

func (s *Storage) connect() error {
	db, err := sql.Open(DatabaseDriverName, fmt.Sprintf("file:%s?_foreign_keys=1&cache=shared", s.path))
	if err != nil {
		return err
	}
	s.db = sqlx.NewDb(db, "sqlite3")

	// trigger connection hook
	if err := db.Ping(); err != nil {
		return err
	}
	s.conn = <-connections

	s.db.SetMaxOpenConns(1)

	return nil
}

func (s *Storage) runQueryList(queries []string) error {
	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

func (s *Storage) isNew() (bool, error) {
	var names []string
	if err := s.db.Select(&names, "SELECT name FROM sqlite_master WHERE type = 'table'"); err != nil && err != sql.ErrNoRows {
		return false, err
	}
	return (len(names) == 0), nil
}

func (s *Storage) checkApplicationID() error {
	var app_id int32
	if err := s.db.Get(&app_id, "PRAGMA application_id"); err != nil {
		return err
	} else if app_id != ApplicationFileID {
		return fmt.Errorf("database is not a valid DAB database (found application ID 0x%x instead of 0x%x)", app_id, ApplicationFileID)
	}
	return nil
}

func (s *Storage) getVersion() SemVer {
	var int_version int32
	s.autofatal(s.db.Get(&int_version, "PRAGMA user_version"))
	return SemVerFromInt32(int_version)
}

func (s *Storage) setVersion(version SemVer) error {
	// String interpolation is needed because the driver for SQLite doesn't manage this case
	_, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version.ToInt32()))
	return err
}

func (s *Storage) checkVersion(found_version SemVer) error {
	if found_version.Equal(SemVer{0, 0, 0}) {
		return errors.New("database already has tables but no version is set, refusing to continue")
	}

	if !Version.Equal(found_version) {
		if Version.After(found_version) {
			s.logger.Infof("database last written by previous version %s", found_version)
		} else {
			return fmt.Errorf("database last written by version %s more recent than current version", found_version)
		}
	}
	return nil
}

func (s *Storage) migrate(from SemVer, migrations []storageMigration) error {
	for _, migration := range migrations {
		// The migrations are supposed to be sorted from lowest to highest version,
		// so there's no point in having a stop condition.
		if migration.From.Equal(from) || migration.From.After(from) {
			s.logger.Infof("migrating database from version %s to %s", migration.From, migration.To)
			if err := migration.Do(s); err != nil {
				return err
			}
			s.logger.Infof("migration from version %s to %s successful", migration.From, migration.To)
			// Set new version in case there's an error in the next loop,
			// so that the user can easily retry the migration.
			if err := s.setVersion(migration.To); err != nil {
				return err
			}
		}
	}
	return nil
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

func (s *Storage) startupChecks() error {
	if errs := s.foreignKeysCheck(); len(errs) > 0 {
		return fmt.Errorf("foreign key error: \n%v", ErrorsToError(errs, "\n\t"))
	}
	if errs := s.quickCheck(); len(errs) > 0 {
		return fmt.Errorf("integrity check error: \n%v", ErrorsToError(errs, "\n\t"))
	}
	return nil
}

func (s *Storage) init() error {
	if err := s.runQueryList(initQueries); err != nil {
		return err
	}

	s.cache.KnownObjects = NewSyncSet()
	known_objects, err := s.getKnownObjects()
	if err != nil {
		return err
	}
	s.cache.KnownObjects.MultiPut(known_objects)
	s.cache.SubPostIDs = make(map[string]*SyncSet)
	s.cache.SubPostIDsLock = sync.Mutex{}

	return nil
}

func (s *Storage) autofatal(err error) {
	if err != nil {
		s.logger.Fatal(err)
	}
}

/***********
 Maintenance
************/

func (s *Storage) foreignKeysCheck() []error {
	checks := []foreignKeyCheck{}
	if err := s.db.Select(&checks, "PRAGMA foreign_key_check"); err == sql.ErrNoRows {
		return []error{}
	} else if err != nil {
		s.logger.Fatal(err)
	}

	errs := make([]error, 0, len(checks))
	for i, check := range checks {
		var err error
		if check.RowID.Valid {
			err = fmt.Errorf("#%d: row #%d in %s failed to reference key #%v in %s",
				i, check.RowID.Int64, check.Table, check.ForeignKeyID, check.Parent)
		} else {
			err = fmt.Errorf("#%d: a row in %s failed to reference key #%v in %s",
				i, check.Table, check.ForeignKeyID, check.Parent)
		}
		errs = append(errs, err)
	}
	return errs
}

func (s *Storage) quickCheck() []error {
	var results []string
	s.autofatal(s.db.Select(&results, "PRAGMA quick_check"))
	if results[0] == "ok" {
		return []error{}
	}
	errs := make([]error, 0, len(results))
	for _, err := range results {
		errs = append(errs, errors.New(err))
	}
	return errs
}

func (s *Storage) PeriodicCleanup(ctx context.Context) error {
	for SleepCtx(ctx, s.cleanupInterval) {
		s.logger.Debug("performing database vacuum")
		if _, err := s.db.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
			return err
		}
		s.logger.Debug("performing database optimization")
		if _, err := s.db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *Storage) Backup() error {
	s.backup.Lock()
	defer s.backup.Unlock()
	s.logger.Debugf("running database backup at %s", s.backup.Path)

	if older, err := FileOlderThan(s.backup.Path, s.backup.MaxAge); err != nil {
		return err
	} else if !older {
		s.logger.Debugf("database backup was not older than %v, nothing was done", s.backup.MaxAge)
		return nil
	}

	s.logger.Debugf("opening connection at %s for backup", s.backup.Path)
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
	s.logger.Debugf("backup connection at %s ok", s.backup.Path)

	backup, err := dest_conn.Backup("main", s.conn, "main") // yes, in *that* order!
	if err != nil {
		return err
	}
	defer backup.Close()

	for done := false; !done; {
		s.logger.Debug("trying to backup all pages")
		done, err = backup.Step(-1) // -1 means to save all remaining pages
		if err != nil {
			return err
		}
	}
	s.logger.Debug("backup done")
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
	s.autofatal(err)
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
	return s.simpleEditUser("UPDATE user_archive SET deleted = TRUE WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) HideUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET hidden = TRUE WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) UnHideUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET hidden = FALSE WHERE name = ? COLLATE NOCASE", username)
}

func (s *Storage) SuspendUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET suspended = TRUE WHERE name = ?", username)
}

func (s *Storage) UnSuspendUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET suspended = FALSE WHERE name = ?", username)
}

func (s *Storage) NotFoundUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET not_found = TRUE WHERE name = ?", username)
}

func (s *Storage) FoundUser(username string) error {
	return s.simpleEditUser("UPDATE user_archive SET not_found = FALSE WHERE name = ?", username)
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
	s.autofatal(err)
	return users
}

func (s *Storage) ListUsers() []User {
	return s.anyListUsers("SELECT * FROM users WHERE suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

func (s *Storage) ListSuspendedAndNotFound() []User {
	return s.anyListUsers("SELECT * FROM users WHERE suspended IS TRUE OR not_found IS TRUE ORDER BY last_scan")
}

func (s *Storage) ListActiveUsers() []User {
	return s.anyListUsers("SELECT * FROM users WHERE inactive IS FALSE AND suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
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

// This method saves comments of a single user, and updates the user's metadata according
// to the properties of the list of comments. It returns the updated User datastructure to
// avoid having to do another request to get the update.
// What happens here controls how the scanner will behave next.
//
// Make sure the comments are all from the same user and the user struct is up to date.
// This method may seem to have a lot of logic for something in the storage layer,
// but most of it used to be in the scanner for reddit and outside of a transaction;
// putting the data-consistency related logic here simplifies greatly the overall code.
func (s *Storage) SaveCommentsUpdateUser(comments []Comment, user User, max_age time.Duration) (User, error) {
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

	// We need to know how many relevant we got, and save it in the user's metadata.
	// This way, the scanner can avoid fetching superfluous comments.
	now := time.Now().Round(0)
	user.BatchSize = 0
	for _, comment := range comments {
		if now.Sub(comment.CreatedTime()) < max_age {
			user.BatchSize++
		}
	}

	if user.New && user.Position == "" { // end of the listing reached
		tx.MustExec("UPDATE user_archive SET new = FALSE WHERE name = ?", user.Name)
	}

	if !user.New && user.BatchSize < uint(len(comments)) { // position resetting doesn't apply to new users
		user.Position = ""
	}

	// All comments are younger than max_age, there may be more.
	if user.BatchSize == uint(len(comments)) {
		user.BatchSize = MaxRedditListingLength
	}

	user.LastScan = now.Unix()

	query := "UPDATE user_archive SET position = ?, batch_size = ?, last_scan = ? WHERE name = ?"
	tx.MustExec(query, user.Position, user.BatchSize, user.LastScan, user.Name)

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
			AND users.hidden IS FALSE
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

func (s *Storage) StatsBetween(score int64, since, until time.Time) UserStatsMap {
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
			AND users.hidden IS FALSE
			AND comments.created BETWEEN ? AND ?
		GROUP BY comments.author
		HAVING MIN(comments.score) <= ?`)
	s.autofatal(err)
	defer stmt.Close()

	rows, err := stmt.Query(since.Unix(), until.Unix(), score)
	s.autofatal(err)
	defer rows.Close()

	var stats = UserStatsMap{}
	for rows.Next() {
		var data UserStats
		s.autofatal(rows.Scan(&data.Name, &data.Average, &data.Delta, &data.Count))
		stats[data.Name] = data
	}
	s.autofatal(rows.Err())

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
