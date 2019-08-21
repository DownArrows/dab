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
	Retry           RetryConf
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

// GetConn returns an SQLiteConn.
func (db *SQLiteDatabase) GetConn(ctx context.Context) (*SQLiteConn, error) {
	return NewSQLiteConn(ctx, db.logger, db.getConnDefaultOptions())
}

func (db *SQLiteDatabase) getConnDefaultOptions() SQLiteConnOptions {
	return SQLiteConnOptions{
		RetryConf:   db.Retry,
		ForeignKeys: true,
		Path:        db.Path,
		OpenOptions: SQLiteDefaultOpenOptions,
		Timeout:     db.Timeout,
	}
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
	err := conn.Select("PRAGMA application_id", func(stmt *SQLiteStmt) error {
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
	err := conn.Select("PRAGMA user_version", func(stmt *SQLiteStmt) error {
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
	err := conn.Select("PRAGMA foreign_key_check", func(stmt *SQLiteStmt) error {
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
	err := conn.Select("PRAGMA quick_check", func(stmt *SQLiteStmt) error {
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

	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	for SleepCtx(ctx, db.CleanupInterval) {
		db.logger.Debugf("performing database %p at %q vacuum", db, db.Path)
		if err := conn.Exec("PRAGMA incremental_vacuum"); err != nil {
			return err
		}
		db.logger.Debugf("performing database %p at %q optimization", db, db.Path)
		if err := conn.Exec("PRAGMA optimize"); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Backup creates or clobbers a backup of the current database at the destination set in the SQLiteBackupOptinos.
func (db *SQLiteDatabase) Backup(ctx context.Context, srcConn *SQLiteConn, opts SQLiteBackupOptions) error {
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
func (fkc *SQLiteForeignKeyCheck) FromDB(stmt *SQLiteStmt) error {
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
	RetryConf
	ForeignKeys bool
	OpenOptions int
	Path        string
	Timeout     time.Duration
}

// SQLiteConn is a single connection to an SQLite database.
// If you want to share one between several goroutines,
// use its sync.Locker interface.
type SQLiteConn struct {
	SQLiteConnOptions
	sync.Mutex
	mutex  *sync.Mutex
	closed bool
	conn   *sqlite.Conn
	ctx    context.Context
	logger LevelLogger
	Path   string
}

// NewSQLiteConn creates a connection to a SQLite database.
// Note that the timeout isn't taken into account for this phase;
// it will return a "database locked" error if it can't immediately connect.
func NewSQLiteConn(ctx context.Context, logger LevelLogger, conf SQLiteConnOptions) (*SQLiteConn, error) {
	sc := &SQLiteConn{
		SQLiteConnOptions: conf,

		ctx:    ctx,
		logger: logger,
		mutex:  &sync.Mutex{},
		Path:   conf.Path,
	}

	err := sc.retry(func() error {
		sc.logger.Debugf("trying to connect SQLite connection %p to %q", sc, sc.Path)
		conn, err := sqlite.Open(conf.Path, conf.OpenOptions)
		if err != nil {
			return err
		}
		sc.conn = conn
		sc.conn.BusyFunc(sc.busy)
		if conf.ForeignKeys {
			return conn.Exec("PRAGMA foreign_keys = ON")
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	sc.logger.Debugf("created SQLite connection %p to %q", sc, sc.Path)
	return sc, nil
}

func (sc *SQLiteConn) busy(count int) bool {
	sc.logger.Infof("%p calling busy function with count %d", sc, count)
	if sc.Times > -1 && count > sc.Times {
		return false
	}
	// ignore its result because we don't want to trigger a busy error because of a cancellation,
	// that would break the semantics of the error for our application
	SleepCtx(sc.ctx, sc.Timeout)
	return true
}

func (sc *SQLiteConn) retry(cb func() error) error {
	return NewRetrier(sc.RetryConf, sc.logRetry).SetErrorFilter(isSQLiteBusyErr).Set(WithCtx(cb)).Task(sc.ctx)
}

func (sc *SQLiteConn) logRetry(r *Retrier, err error) {
	sc.logger.Errorf("SQLite connection %p at %q got an error, retrying: %v", sc, sc.Path, err)
}

// Close idempotently closes the connection.
func (sc *SQLiteConn) Close() error {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	if sc.closed {
		return nil
	}
	sc.closed = true
	sc.logger.Debugf("closing SQLite connection %p to %q", sc, sc.Path)
	err := sc.conn.Close()
	sc.logger.Debugf("closed SQLite connection %p to %q with error %v", sc, sc.Path, err)
	return err
}

// Changes returns the number of rows that have been changed by the last queries.
func (sc *SQLiteConn) Changes() int {
	return sc.conn.Changes()
}

// Backup backs the database up using the given connection to the backup file.
// srcName is the name of the database inside the database file, which is relevant if you attach secondary databases;
// otherwise it is "main". Similarly for destName, it is the target name of the database inside the destination file.
func (sc *SQLiteConn) Backup(srcName string, conn *SQLiteConn, destName string) (*SQLiteBackup, error) {
	var sqltBackup *sqlite.Backup
	err := sc.retry(func() error {
		var err error
		sqltBackup, err = sc.conn.Backup(srcName, conn.conn, destName)
		return err
	})
	if err != nil {
		return nil, err
	}
	backup := &SQLiteBackup{
		RetryConf: sc.RetryConf,
		backup:    sqltBackup,
		ctx:       sc.ctx,
		destPath:  conn.Path,
		logger:    sc.logger,
		srcPath:   sc.Path,
	}
	return backup, nil
}

// Prepare prepares an SQL statement and binds some arguments (none to all).
func (sc *SQLiteConn) Prepare(sql string, args ...interface{}) (*SQLiteStmt, error) {
	var sqltStmt *sqlite.Stmt
	err := sc.retry(func() error {
		var err error
		sc.logger.Debugf("preparing SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.Path)
		sqltStmt, err = sc.conn.Prepare(sql, args...)
		return err
	})
	stmt := &SQLiteStmt{
		ctx:       sc.ctx,
		logger:    sc.logger,
		RetryConf: sc.RetryConf,
		stmt:      sqltStmt,
	}
	sc.logger.Debugf("prepared SQL statement %p with query |%v| and arguments %v for connection %p on %q: %+v", stmt, sql, args, sc, sc.Path, stmt)
	return stmt, err
}

// Select runs an SQL statement witih the given arguments and lets a callback read the statement
// to get its result until all rows in the response are read, and closes the statement.
func (sc *SQLiteConn) Select(sql string, cb func(*SQLiteStmt) error, args ...interface{}) error {
	return sc.retry(func() error {
		sc.logger.Debugf("row scan from SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.Path)
		stmt, err := sc.Prepare(sql, args...)
		if err != nil {
			return err
		}
		defer stmt.Close()
		return stmt.Scan(cb)
	})
}

// Exec execute the SQL statement with the given arguments, managing the entirety of the underlying statement's lifecycle.
func (sc *SQLiteConn) Exec(sql string, args ...interface{}) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.Path)
		return sc.conn.Exec(sql, args...)
	})
}

// MultiExec execute multiple SQL statements with their arguments.
func (sc *SQLiteConn) MultiExec(queries []SQLQuery) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing with SQLite connection %p at %q multiple SQL queries: %+v", sc, sc.Path, queries)
		for _, query := range queries {
			if err := sc.Exec(query.SQL, query.Args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// WithTx runs a callback while managing a transaction's entire lifecycle.
func (sc *SQLiteConn) WithTx(cb func() error) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing SQL transaction with connection %p on %q", sc, sc.Path)
		return sc.conn.WithTx(cb)
	})
}

// MultiExecWithTx is like MultiExec but within a single transaction.
func (sc *SQLiteConn) MultiExecWithTx(queries []SQLQuery) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing with SQLite connection %p at %q within a transaction multiple SQL queries: %+v", sc, sc.Path, queries)
		return sc.conn.WithTx(func() error { return sc.MultiExec(queries) })
	})
}

// SQLiteBackup is a wrapper for *sqlite.Backup with retries.
type SQLiteBackup struct {
	RetryConf
	backup   *sqlite.Backup
	ctx      context.Context
	destPath string
	logger   LevelLogger
	srcPath  string
}

// Close closes the backup.
func (b *SQLiteBackup) Close() error {
	return b.backup.Close()
}

// Step saves n pages; returns io.EOF when finished.
func (b *SQLiteBackup) Step(n int) error {
	cb := func() error { return b.backup.Step(n) }
	return NewRetrier(b.RetryConf, b.logRetry).SetErrorFilter(isSQLiteBusyErr).Set(WithCtx(cb)).Task(b.ctx)
}

func (b *SQLiteBackup) logRetry(r *Retrier, err error) {
	b.logger.Debugf("error when saving pages with SQLite backup %p from %s to %s, retrying: %v", b, b.srcPath, b.destPath, err)
	b.logger.Errorf("error with a database backup from %s to %s, retrying (%d/%d, backoff %s): %v",
		b.srcPath, b.destPath, r.Retries, r.Times, r.Backoff, err)
}

// SQLiteStmt is a wrapper for sqlite.Stmt that supports automatic retry with busy errors.
type SQLiteStmt struct {
	RetryConf
	ctx    context.Context
	logger LevelLogger
	stmt   *sqlite.Stmt
}

// Close closes the statement (simple wrapper).
func (stmt *SQLiteStmt) Close() error {
	return stmt.stmt.Close()
}

// ColumnText returns a string for the given column number, starting at 0 (wrapper with retry).
func (stmt *SQLiteStmt) ColumnText(pos int) (string, bool, error) {
	var result string
	var ok bool
	err := stmt.retry(func() error {
		var err error
		result, ok, err = stmt.stmt.ColumnText(pos)
		return err
	})
	return result, ok, err
}

// ColumnInt returns an integer for the given column number, starting at 0 (wrapper with retry).
func (stmt *SQLiteStmt) ColumnInt(pos int) (int, bool, error) {
	var result int
	var ok bool
	err := stmt.retry(func() error {
		var err error
		result, ok, err = stmt.stmt.ColumnInt(pos)
		return err
	})
	return result, ok, err
}

// ColumnInt64 returns a 64 bits integer for the given column number, starting at 0 (wrapper with retry).
func (stmt *SQLiteStmt) ColumnInt64(pos int) (int64, bool, error) {
	var result int64
	var ok bool
	err := stmt.retry(func() error {
		var err error
		result, ok, err = stmt.stmt.ColumnInt64(pos)
		return err
	})
	return result, ok, err
}

// ColumnDouble returns a 64 bits float for the given column number, starting at 0 (wrapper with retry).
func (stmt *SQLiteStmt) ColumnDouble(pos int) (float64, bool, error) {
	var result float64
	var ok bool
	err := stmt.retry(func() error {
		var err error
		result, ok, err = stmt.stmt.ColumnDouble(pos)
		return err
	})
	return result, ok, err
}

// Step advances the scan of the result rows by one, true if successful, false if end reached (wrapper with retry).
func (stmt *SQLiteStmt) Step() (bool, error) {
	var ok bool
	err := stmt.retry(func() error {
		var err error
		ok, err = stmt.stmt.Step()
		return err
	})
	return ok, err
}

// Exec execute the statements with the given supplementary arguments (wrapper with retry).
func (stmt *SQLiteStmt) Exec(args ...interface{}) error {
	return stmt.retry(func() error { return stmt.stmt.Exec(args...) })
}

// ClearBindings clears the bindings of the previous Exec (wrapper with retry).
func (stmt *SQLiteStmt) ClearBindings() error {
	return stmt.retry(func() error { return stmt.stmt.ClearBindings() })
}

// Scan runs a callback multiple times onto the SQLite statement to read its results until every record has been read.
func (stmt *SQLiteStmt) Scan(cb func(*SQLiteStmt) error) error {
	for {
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

func (stmt *SQLiteStmt) retry(cb func() error) error {
	return NewRetrier(stmt.RetryConf, stmt.logRetry).SetErrorFilter(isSQLiteBusyErr).Set(WithCtx(cb)).Task(stmt.ctx)
}

func (stmt *SQLiteStmt) logRetry(r *Retrier, err error) {
	stmt.logger.Debugf("error in SQLite statement %p, retrying: %v", stmt, err)
	stmt.logger.Errorf("error within a database connection, retrying (%d/%d, backoff %s): %v",
		r.Retries, r.Times, r.Backoff, err)
}

// SQLiteConnPool is a simple pool with a limited size.
// Do not release more conns than the set size, there's no check,
// and it will not behave properly relatively to cancellation.
type SQLiteConnPool struct {
	ch   chan *SQLiteConn
	ctx  context.Context
	size uint
}

// NewSQLiteConnPool creates a new SQLiteConnPool with a global context.
func NewSQLiteConnPool(ctx context.Context, size uint) *SQLiteConnPool {
	return &SQLiteConnPool{
		ch:   make(chan *SQLiteConn, size),
		ctx:  ctx,
		size: size,
	}
}

// Acquire gets a connection or waits until it is cancelled or receives a connection.
func (pool *SQLiteConnPool) Acquire(ctx context.Context) (*SQLiteConn, error) {
	select {
	case <-pool.ctx.Done():
		return nil, pool.ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	case conn := <-pool.ch:
		return conn, nil
	}
}

// Release puts a connection back into the pool.
func (pool *SQLiteConnPool) Release(conn *SQLiteConn) {
	pool.ch <- conn
}

// Close closes all connections; Release panics after this.
func (pool *SQLiteConnPool) Close() error {
	errs := NewErrorGroup()
	nb := uint(0)
	close(pool.ch)
	for conn := range pool.ch {
		if err := conn.Close(); err != nil {
			errs.Add(err)
		}
		nb++
	}
	if nb != pool.size {
		errs.Add(fmt.Errorf("connection pool closed %d on the expected %d", nb, pool.size))
	}
	return errs.ToError()
}

func isSQLiteBusyErr(err error) bool {
	sqliteErr, ok := err.(*sqlite.Error)
	if !ok || sqliteErr == nil {
		return false
	}
	code := sqliteErr.Code()
	return (code == sqlite.LOCKED_SHAREDCACHE || code == sqlite.BUSY || code == sqlite.IOERR_LOCK)
}
