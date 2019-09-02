package main

import (
	"context"
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"sync"
	"time"
)

// SQLiteConn is the interface for a connection to SQLite.
// An interface allows for extensions of a base type.
type SQLiteConn interface {
	sync.Locker
	// Base returns the actual data structure implementing the driver.
	Base() *sqlite.Conn
	// Path is the path of the database this connection is opened on.
	Path() string
	// Close idempotently closes the connection.
	Close() error
	// Changes returns the number of rows that have been changed by the last queries.
	Changes() int
	// ReadUncommitted sets whether the connection is allowed to read uncommitted data from the database.
	ReadUncommitted(bool) error
	// Backup backs the database up using the given connection to the backup file.
	// srcName is the name of the database inside the database file, which is relevant if you attach secondary databases;
	// otherwise it is "main". Similarly for destName, it is the target name of the database inside the destination file.
	Backup(srcName string, destConn SQLiteConn, destName string) (*SQLiteBackup, error)
	// Prepare prepares an SQL statement and binds some arguments (none to all).
	Prepare(string, ...interface{}) (*SQLiteStmt, error)
	// Select runs an SQL statement witih the given arguments and lets a callback read the statement
	// to get its result until all rows in the response are read, and closes the statement.
	Select(string, func(*SQLiteStmt) error, ...interface{}) error
	// Exec execute the SQL statement with the given arguments, managing the entirety of the underlying statement's lifecycle.
	Exec(string, ...interface{}) error
	// MultiExec execute multiple SQL statements with their arguments.
	MultiExec([]SQLQuery) error
	// WithTx runs a callback while managing a transaction's entire lifecycle.
	WithTx(func() error) error
	// MultiExecWithTx is like MultiExec but within a single transaction.
	MultiExecWithTx([]SQLQuery) error
}

// SQLiteConnOptions describes the connection options for an SQLiteConn.
type SQLiteConnOptions struct {
	ForeignKeys bool
	Retry       RetryConf
	OpenOptions int
	Path        string
	Timeout     time.Duration
}

// BaseSQLiteConn is a single connection to an SQLite database.
// If you want to share one between several goroutines,
// use its sync.Locker interface.
// Make sure it can always be copied.
type BaseSQLiteConn struct {
	sync.Mutex
	SQLiteConnOptions
	path   string
	closed bool
	conn   *sqlite.Conn
	ctx    context.Context
	logger LevelLogger
	mutex  *sync.Mutex
}

// NewBaseSQLiteConn creates a connection to a SQLite database.
// Note that the timeout isn't taken into account for this phase;
// it will return a "database locked" error if it can't immediately connect.
func NewBaseSQLiteConn(ctx context.Context, logger LevelLogger, conf SQLiteConnOptions) (*BaseSQLiteConn, error) {
	sc := &BaseSQLiteConn{
		SQLiteConnOptions: conf,

		path:   conf.Path,
		ctx:    ctx,
		logger: logger,
		mutex:  &sync.Mutex{},
	}

	err := sc.retry(func() error {
		sc.logger.Debugf("trying to connect SQLite connection %p to %q", sc, sc.path)
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

	sc.logger.Debugf("created SQLite connection %p to %q", sc, sc.path)
	return sc, nil
}

// Base implements SQLiteConn.
func (sc *BaseSQLiteConn) Base() *sqlite.Conn {
	return sc.conn
}

// Path implements SQLiteConn.
func (sc *BaseSQLiteConn) Path() string {
	return sc.path
}

// Close implements SQLiteConn.
func (sc *BaseSQLiteConn) Close() error {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	if sc.closed {
		return nil
	}
	sc.closed = true
	sc.logger.Debugf("closing SQLite connection %p to %q", sc, sc.path)
	err := sc.conn.Close()
	sc.logger.Debugf("closed SQLite connection %p to %q with error %v", sc, sc.path, err)
	return err
}

// Changes implements SQLiteConn.
func (sc *BaseSQLiteConn) Changes() int {
	return sc.conn.Changes()
}

// ReadUncommitted implements SQLiteConn.
func (sc *BaseSQLiteConn) ReadUncommitted(set bool) error {
	return sc.Exec(fmt.Sprintf("PRAGMA read_uncommitted = %t", set))
}

// Backup implements SQLiteConn.
func (sc *BaseSQLiteConn) Backup(srcName string, conn SQLiteConn, destName string) (*SQLiteBackup, error) {
	var sqltBackup *sqlite.Backup
	err := sc.retry(func() error {
		var err error
		sqltBackup, err = sc.conn.Backup(srcName, conn.Base(), destName)
		return err
	})
	if err != nil {
		return nil, err
	}
	backup := &SQLiteBackup{
		Retry:    sc.Retry,
		backup:   sqltBackup,
		ctx:      sc.ctx,
		destPath: conn.Path(),
		logger:   sc.logger,
		srcPath:  sc.Path(),
	}
	return backup, nil
}

// Prepare implements SQLiteConn.
func (sc *BaseSQLiteConn) Prepare(sql string, args ...interface{}) (*SQLiteStmt, error) {
	var sqltStmt *sqlite.Stmt
	err := sc.retry(func() error {
		var err error
		sc.logger.Debugf("preparing SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.path)
		sqltStmt, err = sc.conn.Prepare(sql, args...)
		return err
	})
	stmt := &SQLiteStmt{
		ctx:    sc.ctx,
		logger: sc.logger,
		Retry:  sc.Retry,
		stmt:   sqltStmt,
	}
	sc.logger.Debugf("prepared SQL statement %p with query |%v| and arguments %v for connection %p on %q: %+v", stmt, sql, args, sc, sc.path, stmt)
	return stmt, err
}

// Select implements SQLiteConn.
func (sc *BaseSQLiteConn) Select(sql string, cb func(*SQLiteStmt) error, args ...interface{}) error {
	return sc.retry(func() error {
		sc.logger.Debugf("row scan from SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.path)
		stmt, err := sc.Prepare(sql, args...)
		if err != nil {
			return err
		}
		defer stmt.Close()
		return stmt.Scan(cb)
	})
}

// Exec implements SQLiteConn.
func (sc *BaseSQLiteConn) Exec(sql string, args ...interface{}) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing SQL statement |%v| with arguments %v for connection %p on %q", sql, args, sc, sc.path)
		return sc.conn.Exec(sql, args...)
	})
}

// MultiExec implements SQLiteConn.
func (sc *BaseSQLiteConn) MultiExec(queries []SQLQuery) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing with SQLite connection %p at %q multiple SQL queries: %+v", sc, sc.path, queries)
		for _, query := range queries {
			if err := sc.Exec(query.SQL, query.Args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// WithTx implements SQLiteConn.
func (sc *BaseSQLiteConn) WithTx(cb func() error) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing SQL transaction with connection %p on %q", sc, sc.path)
		return sc.conn.WithTx(cb)
	})
}

// MultiExecWithTx implements SQLiteConn.
func (sc *BaseSQLiteConn) MultiExecWithTx(queries []SQLQuery) error {
	return sc.retry(func() error {
		sc.logger.Debugf("executing with SQLite connection %p at %q within a transaction multiple SQL queries: %+v", sc, sc.path, queries)
		return sc.conn.WithTx(func() error { return sc.MultiExec(queries) })
	})
}

func (sc *BaseSQLiteConn) busy(count int) bool {
	sc.logger.Infof("%p calling busy function with count %d", sc, count)
	if sc.Retry.Times > -1 && count > sc.Retry.Times {
		return false
	}
	// ignore its result because we don't want to trigger a busy error because of a cancellation,
	// that would break the semantics of the error for our application
	SleepCtx(sc.ctx, sc.Timeout)
	return true
}

func (sc *BaseSQLiteConn) retry(cb func() error) error {
	return NewRetrier(sc.Retry, sc.logRetry).SetErrorFilter(isSQLiteBusyErr).Set(WithCtx(cb)).Task(sc.ctx)
}

func (sc *BaseSQLiteConn) logRetry(r *Retrier, err error) {
	sc.logger.Errorf("SQLite connection %p at %q got an error, retrying: %v", sc, sc.path, err)
}

// SQLiteBackup is a wrapper for *sqlite.Backup with retries.
type SQLiteBackup struct {
	Retry    RetryConf
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
	return NewRetrier(b.Retry, b.logRetry).SetErrorFilter(isSQLiteBusyErr).Set(WithCtx(cb)).Task(b.ctx)
}

func (b *SQLiteBackup) logRetry(r *Retrier, err error) {
	b.logger.Debugf("error when saving pages with SQLite backup %p from %s to %s, retrying: %v", b, b.srcPath, b.destPath, err)
	b.logger.Errorf("error with a database backup from %s to %s, retrying (%d/%d, backoff %s): %v",
		b.srcPath, b.destPath, r.Retries, r.Times, r.Backoff, err)
}

// SQLiteStmt is a wrapper for sqlite.Stmt that supports automatic retry with busy errors.
type SQLiteStmt struct {
	Retry  RetryConf
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
	return NewRetrier(stmt.Retry, stmt.logRetry).SetErrorFilter(isSQLiteBusyErr).Set(WithCtx(cb)).Task(stmt.ctx)
}

func (stmt *SQLiteStmt) logRetry(r *Retrier, err error) {
	stmt.logger.Debugf("error in SQLite statement %p, retrying: %v", stmt, err)
	stmt.logger.Errorf("error within a database connection, retrying (%d/%d, backoff %s): %v",
		r.Retries, r.Times, r.Backoff, err)
}

// IsSQLiteForeignKeyErr tests if the error is an error with a foreign key constraint.
func IsSQLiteForeignKeyErr(err error) bool {
	sqliteErr, ok := err.(*sqlite.Error)
	if !ok || sqliteErr == nil {
		return false
	}
	return sqliteErr.Code() == sqlite.CONSTRAINT_FOREIGNKEY
}

func isSQLiteBusyErr(err error) bool {
	sqliteErr, ok := err.(*sqlite.Error)
	if !ok || sqliteErr == nil {
		return false
	}
	code := sqliteErr.Code()
	return (code == sqlite.LOCKED_SHAREDCACHE || code == sqlite.BUSY || code == sqlite.IOERR_LOCK)
}
