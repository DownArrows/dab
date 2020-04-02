package main

import (
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

// IsSQLiteForeignKeyError indicates whether the given error is an error about a missing foreign key.
func IsSQLiteForeignKeyError(err error) bool {
	errSQLite, ok := err.(*sqlite.Error)
	if !ok {
		return false
	}
	return errSQLite.Code() == sqlite.CONSTRAINT_FOREIGNKEY
}

// IsSQLitePrimaryKeyConstraintError indicates whether the given error is an error about the constraints on a primary key.
func IsSQLitePrimaryKeyConstraintError(err error) bool {
	errSQLite, ok := err.(*sqlite.Error)
	if !ok {
		return false
	}
	return errSQLite.Code() == sqlite.CONSTRAINT_PRIMARYKEY
}

// SQLQuery describes multiple SQL queries and their arguments.
type SQLQuery struct {
	SQL  string
	Args []Any
}

// SQLiteMigration describes a migration from a SemVer to another.
type SQLiteMigration struct {
	From SemVer
	To   SemVer
	Exec func(SQLiteConn) error
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
	InitHook        func(SQLiteConn) error
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
// It returns the connection it needed to run the checks; if you are using a temporary database, keep it open until shut down.
// It treats already existing empty files as new databases.
func NewSQLiteDatabase(ctx Ctx, logger LevelLogger, opts SQLiteDatabaseOptions) (*SQLiteDatabase, SQLiteConn, error) {
	db := &SQLiteDatabase{
		SQLiteDatabaseOptions: opts,

		backups: sync.Mutex{},
		logger:  logger,
	}

	db.logger.Debugf("opening %s, version %s, application ID 0x%x, cleanup interval %s",
		db, db.Version, db.AppID, db.CleanupInterval)

	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, nil, err
	}

	if err := db.init(conn); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return db, conn, nil
}

// GetConn returns an SQLiteConn.
func (db *SQLiteDatabase) GetConn(ctx Ctx) (SQLiteConn, error) {
	return NewBaseSQLiteConn(ctx, db.logger, db.getConnDefaultOptions())
}

func (db *SQLiteDatabase) getConnDefaultOptions() SQLiteConnOptions {
	return SQLiteConnOptions{
		AnalyzeOnClose: true,
		ForeignKeys:    true,
		OpenOptions:    SQLiteDefaultOpenOptions,
		Path:           db.Path,
		Retry:          db.Retry,
		Timeout:        db.Timeout,
	}
}

func (db *SQLiteDatabase) init(conn SQLiteConn) error {
	isNew := false
	if stat, err := os.Stat(db.Path); os.IsNotExist(err) || stat.Size() == 0 {
		isNew = true
		db.logger.Infof("database %q doesn't exist, creating", db.Path)
	} else if err != nil {
		return err
	} else if stat.IsDir() {
		return fmt.Errorf("cannot open %q as a database, it is a directory", db.Path)
	}

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
		} else if err := conn.Analyze(); err != nil {
			return err
		}
		return nil
	})
}

func (db *SQLiteDatabase) checkApplicationID(conn SQLiteConn) error {
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

func (db *SQLiteDatabase) setAppID(conn SQLiteConn) error {
	return conn.Exec(fmt.Sprintf("PRAGMA application_id = %d", db.AppID))
}

func (db *SQLiteDatabase) getWrittenVersion(conn SQLiteConn) error {
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
	db.logger.Debugf("%s found version %s", db, db.WrittenVersion)
	return nil
}

func (db *SQLiteDatabase) String() string {
	return fmt.Sprintf("SQLite database %p at %q", db, db.Path)
}

func (db *SQLiteDatabase) setVersion(conn SQLiteConn, version SemVer) error {
	db.logger.Debugf("%s writing version %s", db, version)
	// String interpolation is needed because the driver for SQLite doesn't deal with this case
	if err := conn.Exec(fmt.Sprintf("PRAGMA user_version = %d", version.ToInt())); err != nil {
		return err
	}
	db.WrittenVersion = version
	return nil
}

func (db *SQLiteDatabase) migrate(conn SQLiteConn) error {
	for _, migration := range db.Migrations {
		// The migrations are supposed to be sorted from lowest to highest version,
		// so there's no point in having a stop condition.
		if migration.From.AfterOrEqual(db.WrittenVersion) && db.Version.AfterOrEqual(migration.To) {
			db.logger.Infof("migrating database %q from version %s to %s", db.Path, migration.From, migration.To)
			if err := migration.Exec(conn); err != nil {
				return err
			}
			db.logger.Infof("migration of database %q from version %s to %s successful", db.Path, migration.From, migration.To)
			// Set new version in case there's an error in the next loop,
			// so that the user can easily retry the migration.
			if err := db.setVersion(conn, migration.To); err != nil {
				return err
			}
		}
	}
	return nil
}

func (db *SQLiteDatabase) foreignKeysCheck(conn SQLiteConn) error {
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
		return fmt.Errorf("foreign key error(s) in database %q: %+v", db.Path, checks)
	}
	return nil
}

func (db *SQLiteDatabase) quickCheck(conn SQLiteConn) error {
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
		return fmt.Errorf("integrity check error(s) in database %q: %v", db.Path, errs)
	}
	return nil
}

// PeriodicCleanup is a Task to be launched independently which periodically optimizes the database,
// according to the interval set in the SQLiteDatabaseOptions.
func (db *SQLiteDatabase) PeriodicCleanup(ctx Ctx) error {
	if !(db.CleanupInterval > 0) {
		return fmt.Errorf("database at %q cannot run periodic cleanup with an interval of %s", db.Path, db.CleanupInterval)
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	for SleepCtx(ctx, db.CleanupInterval) {
		db.logger.Debugf("performing incremental vacuum of %s", db)
		if err := conn.Exec("PRAGMA incremental_vacuum"); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Backup creates or clobbers a backup of the current database at the destination set in the SQLiteBackupOptinos.
func (db *SQLiteDatabase) Backup(ctx Ctx, srcConn SQLiteConn, opts SQLiteBackupOptions) error {
	db.backups.Lock()
	defer db.backups.Unlock()

	db.logger.Debugf("opening connection at %q for backup of database %q from %s", opts.DestPath, opts.SrcName, db)
	destOpts := db.getConnDefaultOptions()
	destOpts.Path = opts.DestPath
	destConn, err := NewBaseSQLiteConn(ctx, db.logger, destOpts)
	if err != nil {
		return err
	}
	defer destConn.Close()

	backup, err := srcConn.Backup(opts.SrcName, destConn, opts.DestName)
	if err != nil {
		return err
	}
	defer backup.Close()

	db.logger.Debugf("with %s, backup connection %+v and %+v from %q to %q established",
		db, srcConn, destConn, db.Path, opts.DestPath)

	for {
		// Surprisingly, this is the best way to avoid getting a "database locked" error,
		// instead of saving a few pages at a time.
		// This is probably due to SQLite's deadlock detetection in its notify API.
		db.logger.Debugf("with %s, backup connection %+v and %+v from %q to %q trying to backup all pages",
			db, srcConn, destConn, db.Path, opts.DestPath)
		err = backup.Step(-1) // -1 saves all remaining pages.
		if err != nil {
			break
		}
	}

	if err != io.EOF {
		db.logger.Debugf("error in %s with backing up with connections %+v and %+v from %q to %q: %v",
			db, srcConn, destConn, db.Path, opts.DestPath, err)
		os.Remove(opts.DestPath)
		return err
	}

	db.logger.Debugf("backup to %q done", opts.DestPath)
	return nil
}

// SQLiteConnPool is a simple pool with a limited size.
// Do not release more conns than the set size, there's no check,
// and it will not behave properly relatively to cancellation.
type SQLiteConnPool struct {
	ch   chan SQLiteConn
	ctx  Ctx
	size uint
}

// NewSQLiteConnPool creates a new SQLiteConnPool with a global context.
func NewSQLiteConnPool(ctx Ctx, size uint, get func(Ctx) (SQLiteConn, error)) (SQLiteConnPool, error) {
	pool := SQLiteConnPool{
		ch:   make(chan SQLiteConn, size),
		ctx:  ctx,
		size: size,
	}
	for i := uint(0); i < pool.size; i++ {
		conn, err := get(ctx)
		if err != nil {
			pool.Close()
			return pool, err
		}
		pool.Release(conn)
	}
	return pool, nil
}

// Analyze runs evenly spaced Analyze on each connection of the pool so that each is analyzed once per the set interval.
func (pool SQLiteConnPool) Analyze(ctx Ctx, interval time.Duration) error {
	for SleepCtx(ctx, interval/time.Duration(pool.size)) {
		pool.WithConn(ctx, func(conn SQLiteConn) error {
			if time.Now().Sub(conn.LastAnalyze()) < interval {
				return nil
			}
			return conn.Analyze()
		})
	}
	return ctx.Err()
}

// Acquire gets a connection or waits until it is cancelled or receives a connection.
func (pool SQLiteConnPool) Acquire(ctx Ctx) (SQLiteConn, error) {
	var conn SQLiteConn
	var err error
	select {
	case <-pool.ctx.Done():
		err = pool.ctx.Err()
	case <-ctx.Done():
		err = ctx.Err()
	case conn = <-pool.ch:
	}
	return conn, err
}

// Release puts a connection back into the pool.
func (pool SQLiteConnPool) Release(conn SQLiteConn) {
	pool.ch <- conn
}

// WithConn automatically acquires and releases a connection.
func (pool SQLiteConnPool) WithConn(ctx Ctx, cb func(SQLiteConn) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer pool.Release(conn)
	return cb(conn)
}

// Close closes all connections; Release panics after this.
func (pool SQLiteConnPool) Close() error {
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
