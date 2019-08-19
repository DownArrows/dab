package main

import (
	"context"
	"errors"
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"io"
	"os"
	"sync"
	"time"
)

// Default options for SQLiteConn.
const (
	SQLiteDefaultOpenOptions = sqlite.OPEN_READWRITE | sqlite.OPEN_CREATE | sqlite.OPEN_NOMUTEX | sqlite.OPEN_SHAREDCACHE | sqlite.OPEN_WAL
	SQLiteDefaultTimeout     = 5 * time.Second
)

// SQLQuery describes multiple SQL queries and their arguments.
type SQLQuery struct {
	SQL  string
	Args []interface{}
}

// SQLiteMigration describes a migration from a SemVer to another.
type SQLiteMigration struct {
	From SemVer
	To   SemVer
	Exec func(*SQLiteConn) error
}

// SQLiteBackupOptions replaces three consecutive string arguments to avoid mistakenly swapping
// the arguments, which the compiler couldn't warn about since they all are of the same type.
// DestName and SrcName are the name of the schema to backup; unless there's an attached
// database to backup, it's always "main" for both.
type SQLiteBackupOptions struct {
	DestName string
	DestPath string
	SrcName  string
}

// SQLiteDatabaseOptions describes the configuration for an SQLite database.
type SQLiteDatabaseOptions struct {
	AppID           int
	CleanupInterval time.Duration
	Migrations      []SQLiteMigration
	Path            string
	Timeout         time.Duration
	Version         SemVer
}

// SQLiteDatabase provides database features that are not application-specific:
//  - open or create a database file with data-safe performance-oriented options
//  - check its application ID and version fields
//  - check its consistency
//  - apply migrations
//  - write the choosen application ID and version
//  - create connections with sane options
//  - a backup method
//  - an autonomous method for recurring cleanup and optimization
type SQLiteDatabase struct {
	SQLiteDatabaseOptions
	// The mutex for backups avoids overwriting a backup while another one is running on the same destination path.
	// Optimally, there should be a mutex for each destination path, but it's unneeded as long as we backup to only
	// one path, or we don't do backups very often to justify the increased complexity.
	backups        sync.Mutex
	logger         LevelLogger
	WrittenVersion SemVer
}

// NewSQLiteDatabase creates a new SQLiteDatabase.
func NewSQLiteDatabase(ctx context.Context, logger LevelLogger, opts SQLiteDatabaseOptions) (*SQLiteDatabase, error) {
	// Supporting both options would mean leave a connection open in its own goroutine;
	// there's no justification for the increased complexity, since there is no use case.
	// Tests can as well use a file that's deleted at the end by custom code.
	if opts.Path == ":memory:" {
		return nil, errors.New("in-memory databases aren't supported")
	} else if opts.Path == "" {
		return nil, errors.New("temporary databases aren't supported")
	}

	db := &SQLiteDatabase{
		backups: sync.Mutex{},
		logger:  logger,
	}

	db.SQLiteDatabaseOptions = opts

	db.logger.Debugf("opening database %p at %q, version %s, application ID 0x%x, cleanup interval %s",
		db, db.Path, db.Version, db.AppID, db.CleanupInterval)

	return db, db.init(ctx)
}

// GetConn returns an SQLiteConn managed by the SQLiteDatabase.
func (db *SQLiteDatabase) GetConn(ctx context.Context) (*SQLiteConn, error) {
	return NewSQLiteConn(ctx, db.logger, db.getConnDefaultOptions())
}

func (db *SQLiteDatabase) getConnDefaultOptions() SQLiteConnOptions {
	return SQLiteConnOptions{
		ForeignKeys: true,
		Path:        db.Path,
		OpenOptions: SQLiteDefaultOpenOptions,
		Timeout:     db.Timeout,
	}
}

// Select is a wrapper for SQLiteConn.Select.
func (db *SQLiteDatabase) Select(ctx context.Context, sql string, cb func(*sqlite.Stmt) error, args ...interface{}) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	err = conn.Select(sql, cb, args...)
	conn.Close()
	return err
}

// Exec is a wrapper for SQLiteConn.Exec.
func (db *SQLiteDatabase) Exec(ctx context.Context, sql string, args ...interface{}) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Exec(sql, args...)
}

// MultiExec is a wrapper for SQLiteConn.MultiExec.
func (db *SQLiteDatabase) MultiExec(ctx context.Context, queries []SQLQuery) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.MultiExec(queries)
}

// WithTx is a wrapper for SQLiteConn.WithTx.
func (db *SQLiteDatabase) WithTx(ctx context.Context, cb func(*SQLiteConn) error) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.WithTx(func() error { return cb(conn) })
}

func (db *SQLiteDatabase) init(ctx context.Context) error {
	isNew := false
	if stat, err := os.Stat(db.Path); os.IsNotExist(err) {
		isNew = true
		db.logger.Infof("database %q doesn't exist, creating", db.Path)
	} else if err != nil {
		return err
	} else if stat.IsDir() {
		return fmt.Errorf("cannot open %q as a database, it is a directory", db.Path)
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if !isNew {
		err := conn.WithTx(func() error {
			if err := db.checkApplicationID(conn); err != nil {
				return err
			} else if err := db.getWrittenVersion(conn); err != nil {
				return err
			} else if db.WrittenVersion.Equal(SemVer{0, 0, 0}) && !db.Version.Equal(SemVer{0, 0, 0}) {
				return fmt.Errorf("database at %q was already created but no version was set", db.Path)
			} else if db.WrittenVersion.After(db.Version) {
				return fmt.Errorf("database at %q was last written by version %s more recent than the current version", db.Path, db.WrittenVersion)
			} else if err := db.quickCheck(conn); err != nil {
				return err
			} else if err := db.foreignKeysCheck(conn); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}

		if db.Version.After(db.WrittenVersion) {
			db.logger.Infof("database at %q was last written by previous version %s", db.Path, db.WrittenVersion)
		}

		// Migrations may need to disable foreign keys, and to do so they have to be outside a transaction.
		if err := db.migrate(conn); err != nil {
			return err
		}

	}

	return conn.WithTx(func() error {
		if err := db.setVersion(conn, db.Version); err != nil {
			return err
		} else if err := db.setAppID(conn); err != nil {
			return err
		} else if err := conn.Exec("PRAGMA auto_vacuum = 'incremental'"); err != nil {
			return err
		}
		return nil
	})
}

func (db *SQLiteDatabase) checkApplicationID(conn *SQLiteConn) error {
	var appID int
	err := conn.Select("PRAGMA application_id", func(stmt *sqlite.Stmt) error {
		var err error
		appID, _, err = stmt.ColumnInt(0)
		return err
	})
	db.logger.Debugf("database %p found application ID 0x%x in %q", db, appID, db.Path)
	if err == nil && appID != db.AppID {
		return fmt.Errorf("database %q is from another application: found application ID 0x%x instead of 0x%x", db.Path, appID, db.AppID)
	}
	return err
}

func (db *SQLiteDatabase) setAppID(conn *SQLiteConn) error {
	return conn.Exec(fmt.Sprintf("PRAGMA application_id = %d", db.AppID))
}

func (db *SQLiteDatabase) getWrittenVersion(conn *SQLiteConn) error {
	var intVersion int
	err := conn.Select("PRAGMA user_version", func(stmt *sqlite.Stmt) error {
		var err error
		intVersion, _, err = stmt.ColumnInt(0)
		return err
	})

	if err != nil {
		return err
	}

	db.WrittenVersion = SemVerFromInt(intVersion)
	db.logger.Debugf("%p found database at %q with version %s", db, db.Path, db.WrittenVersion)
	return nil
}

func (db *SQLiteDatabase) setVersion(conn *SQLiteConn, version SemVer) error {
	db.logger.Debugf("database %p writing version %s at %q", db, version, db.Path)
	// String interpolation is needed because the driver for SQLite doesn't deal with this case
	if err := conn.Exec(fmt.Sprintf("PRAGMA user_version = %d", version.ToInt())); err != nil {
		return err
	}
	db.WrittenVersion = version
	return nil
}

func (db *SQLiteDatabase) migrate(conn *SQLiteConn) error {
	for _, migration := range db.Migrations {
		// The migrations are supposed to be sorted from lowest to highest version,
		// so there's no point in having a stop condition.
		if migration.From.AfterOrEqual(db.WrittenVersion) && db.Version.AfterOrEqual(migration.To) {
			db.logger.Infof("migrating database %p at %q from version %s to %s", db, db.Path, migration.From, migration.To)
			if err := migration.Exec(conn); err != nil {
				return err
			}
			db.logger.Infof("migration of database %p at %q from version %s to %s successful", db, db.Path, migration.From, migration.To)
			// Set new version in case there's an error in the next loop,
			// so that the user can easily retry the migration.
			if err := db.setVersion(conn, migration.To); err != nil {
				return err
			}
		}
	}
	return nil
}

func (db *SQLiteDatabase) foreignKeysCheck(conn *SQLiteConn) error {
	var checks []error
	err := conn.Select("PRAGMA foreign_key_check", func(stmt *sqlite.Stmt) error {
		check := &SQLiteForeignKeyCheck{}
		if err := check.FromDB(stmt); err != nil {
			return err
		}
		checks = append(checks, check)
		return nil
	})
	if err != nil {
		return err
	}
	if len(checks) > 0 {
		return fmt.Errorf("foreign key error(s) in database at %q: %+v", db.Path, checks)
	}
	return nil
}

func (db *SQLiteDatabase) quickCheck(conn *SQLiteConn) error {
	var errs []error
	err := conn.Select("PRAGMA quick_check", func(stmt *sqlite.Stmt) error {
		if msg, _, err := stmt.ColumnText(0); err != nil {
			return err
		} else if msg != "ok" {
			errs = append(errs, errors.New(msg))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return fmt.Errorf("integrity check error(s) in database at %q: %v", db.Path, errs)
	}
	return nil
}

// PeriodicCleanup is a Task to be launched independently which periodically optimizes the database,
// according to the interval set in the SQLiteDatabaseOptions.
func (db *SQLiteDatabase) PeriodicCleanup(ctx context.Context) error {
	if !(db.CleanupInterval > 0) {
		return fmt.Errorf("database at %q cannot run periodic cleanup with an interval of %s", db.Path, db.CleanupInterval)
	}
	for SleepCtx(ctx, db.CleanupInterval) {
		db.logger.Debugf("performing database %p at %q vacuum", db, db.Path)
		if err := db.Exec(ctx, "PRAGMA incremental_vacuum"); err != nil {
			return err
		}
		db.logger.Debugf("performing database %p at %q optimization", db, db.Path)
		if err := db.Exec(ctx, "PRAGMA optimize"); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Backup creates or clobbers a backup of the current database at the destination set in the SQLiteBackupOptinos.
func (db *SQLiteDatabase) Backup(ctx context.Context, opts SQLiteBackupOptions) error {
	db.backups.Lock()
	defer db.backups.Unlock()

	db.logger.Debugf("opening connection at %q for backup from database %p at %q", opts.DestPath, db, db.Path)
	destOpts := db.getConnDefaultOptions()
	destOpts.Path = opts.DestPath
	destConn, err := NewSQLiteConn(ctx, db.logger, destOpts)
	if err != nil {
		return err
	}
	defer destConn.Close()

	srcConn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer srcConn.Close()

	backup, err := srcConn.Backup(opts.SrcName, destConn, opts.DestName)
	if err != nil {
		return err
	}
	defer backup.Close()

	db.logger.Debugf("backup connection %p and %p from %q to %q established",
		srcConn, destConn, db.Path, opts.DestPath)

	for {
		// Surprisingly, this is the best way to avoid getting a "database locked" error,
		// instead of saving a few pages at a time.
		// This is probably due to SQLite's deadlock detetection in its notify API.
		db.logger.Debugf("backup connection %p and %p from %q to %q trying to backup all pages",
			srcConn, destConn, db.Path, opts.DestPath)
		err = backup.Step(-1) // -1 saves all remaning pages.
		if err != nil {
			break
		}
	}

	if err != io.EOF {
		db.logger.Debugf("error with backing up with connections %p and %p from %q to %q: %v", err)
		os.Remove(opts.DestPath)
		return err
	}

	db.logger.Debugf("backup to %q done", opts.DestPath)
	return nil
}

// SQLiteForeignKeyCheck describes a foreign key error in a single row.
type SQLiteForeignKeyCheck struct {
	ValidRowID   bool // RowID can be NULL, contrarily to the rest.
	Table        string
	RowID        int64
	Parent       string
	ForeignKeyID int
}

// FromDB reads the error from the results of "PRAGMA foreign_key_check".
func (fkc *SQLiteForeignKeyCheck) FromDB(stmt *sqlite.Stmt) error {
	var err error
	if fkc.Table, _, err = stmt.ColumnText(0); err != nil {
		return err
	}

	if fkc.RowID, fkc.ValidRowID, err = stmt.ColumnInt64(1); err != nil {
		return err
	}

	if fkc.Parent, _, err = stmt.ColumnText(2); err != nil {
		return err
	}

	fkc.ForeignKeyID, _, err = stmt.ColumnInt(3)
	return err
}

// Error summarizes the error the data structure describes.
func (fkc *SQLiteForeignKeyCheck) Error() string {
	if !fkc.ValidRowID {
		return fmt.Sprintf("a row in %q failed to reference key #%d in %q", fkc.Table, fkc.ForeignKeyID, fkc.Parent)
	}
	return fmt.Sprintf("row #%d in %q failed to reference key #%d in %q", fkc.RowID, fkc.Table, fkc.ForeignKeyID, fkc.Parent)
}

// SQLiteConnOptions describes the connection options for an SQLiteConn.
type SQLiteConnOptions struct {
	ForeignKeys bool
	Path        string
	Timeout     time.Duration
	OpenOptions int
}

// SQLiteConn is a single connection to an SQLite database.
type SQLiteConn struct {
	sync.Mutex
	closed bool
	conn   *sqlite.Conn
	ctx    context.Context
	done   chan struct{}
	logger LevelLogger
	Path   string
}

// NewSQLiteConn creates a connection to a SQLite database.
// Note that the timeout isn't taken into account for this phase;
// it will return a "database locked" error if it can't immediately connect.
func NewSQLiteConn(ctx context.Context, logger LevelLogger, conf SQLiteConnOptions) (*SQLiteConn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	conn, err := sqlite.Open(conf.Path, conf.OpenOptions)
	if err != nil {
		return nil, err
	}

	conn.BusyTimeout(conf.Timeout)

	if conf.ForeignKeys {
		if err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
			return nil, err
		}
	}

	sc := &SQLiteConn{
		conn:   conn,
		done:   make(chan struct{}, 1),
		ctx:    ctx,
		logger: logger,
		Path:   conf.Path,
	}

	go func() {
		select {
		case <-sc.ctx.Done():
			sc.Lock()
			defer sc.Unlock()
			if !sc.closed {
				sc.conn.Interrupt()
			}
		case <-sc.done:
		}
	}()

	sc.logger.Debugf("created SQLite connection %p to %q", sc, sc.Path)
	return sc, nil
}

// Close idempotently closes the connection.
func (sc *SQLiteConn) Close() error {
	sc.Lock()
	defer sc.Unlock()
	if sc.closed {
		return nil
	}
	sc.closed = true
	sc.logger.Debugf("closing SQLite connection %p to %q", sc, sc.Path)
	sc.done <- struct{}{}
	return sc.conn.Close()
}

// TotalChanges returns the number of rows that have been changed.
func (sc *SQLiteConn) TotalChanges() int {
	return sc.conn.TotalChanges()
}

// Backup backs the database up using the given connection to the backup file.
// srcName is the name of the database inside the database file, which is relevant if you attach secondary databases;
// otherwise it is "main". Similarly for destName, it is the target name of the database inside the destination file.
func (sc *SQLiteConn) Backup(srcName string, conn *SQLiteConn, destName string) (*sqlite.Backup, error) {
	if sc.ctx.Err() != nil {
		return nil, sc.ctx.Err()
	}
	return sc.conn.Backup(srcName, conn.conn, destName)
}

// Prepare prepares an SQL statement and binds some arguments (none to all).
func (sc *SQLiteConn) Prepare(sql string, args ...interface{}) (*sqlite.Stmt, error) {
	if sc.ctx.Err() != nil {
		return nil, sc.ctx.Err()
	}
	sc.logger.Debugf("preparing SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.Path)
	return sc.conn.Prepare(sql, args...)
}

// Select runs an SQL statement witih the given arguments and lets a callback read the statement
// to get its result until all rows in the response are read, and closes the statement.
func (sc *SQLiteConn) Select(sql string, cb func(stmt *sqlite.Stmt) error, args ...interface{}) error {
	if sc.ctx.Err() != nil {
		return sc.ctx.Err()
	}
	sc.logger.Debugf("row scan from SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.Path)
	stmt, err := sc.conn.Prepare(sql, args...)
	if err != nil {
		return err
	}
	defer stmt.Close()
	return SQLiteStmtScan(sc.ctx, stmt, cb)
}

// Exec execute the SQL statement with the given arguments, managing the entirety of the underlying statement's lifecycle.
func (sc *SQLiteConn) Exec(sql string, args ...interface{}) error {
	if sc.ctx.Err() != nil {
		return sc.ctx.Err()
	}
	sc.logger.Debugf("executing SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.Path)
	return sc.conn.Exec(sql, args...)
}

// MultiExec execute multiple SQL statements with their arguments.
func (sc *SQLiteConn) MultiExec(queries []SQLQuery) error {
	sc.logger.Debugf("executing with SQLite connection %p at %q multiple SQL queries: %+v", sc, sc.Path, queries)
	for _, query := range queries {
		if err := sc.Exec(query.SQL, query.Args...); err != nil {
			return err
		}
	}
	return nil
}

// WithTx runs a callback while managing a transaction's entire lifecycle.
func (sc *SQLiteConn) WithTx(cb func() error) error {
	if sc.ctx.Err() != nil {
		return sc.ctx.Err()
	}
	sc.logger.Debugf("executing SQL transaction with connection %p on %q", sc, sc.Path)
	return sc.conn.WithTx(cb)
}

// MultiExecWithTx is like MultiExec but within a single transaction.
func (sc *SQLiteConn) MultiExecWithTx(queries []SQLQuery) error {
	if sc.ctx.Err() != nil {
		return sc.ctx.Err()
	}
	sc.logger.Debugf("executing with SQLite connection %p at %q within a transaction multiple SQL queries: %+v", sc, sc.Path, queries)
	return sc.conn.WithTx(func() error { return sc.MultiExec(queries) })
}

// SQLiteStmtScan runs a callback multiple times onto an SQLite statement to read its results until every record has been read.
func SQLiteStmtScan(ctx context.Context, stmt *sqlite.Stmt, cb func(*sqlite.Stmt) error) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ok, err := stmt.Step(); err != nil {
			return err
		} else if !ok {
			break
		}
		if err := cb(stmt); err != nil {
			return err
		}
	}
	return nil
}
