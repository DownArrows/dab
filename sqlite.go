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
func (db *SQLiteDatabase) GetConn(ctx context.Context) (SQLiteConn, error) {
	return NewBaseSQLiteConn(ctx, db.logger, db.getConnDefaultOptions())
}

func (db *SQLiteDatabase) getConnDefaultOptions() SQLiteConnOptions {
	return SQLiteConnOptions{
		Retry:       db.Retry,
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
	db.logger.Debugf("%p found database at %q with version %s", db, db.Path, db.WrittenVersion)
	return nil
}

func (db *SQLiteDatabase) setVersion(conn SQLiteConn, version SemVer) error {
	db.logger.Debugf("database %p writing version %s at %q", db, version, db.Path)
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
		return fmt.Errorf("foreign key error(s) in database at %q: %+v", db.Path, checks)
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
func (db *SQLiteDatabase) Backup(ctx context.Context, srcConn SQLiteConn, opts SQLiteBackupOptions) error {
	db.backups.Lock()
	defer db.backups.Unlock()

	db.logger.Debugf("opening connection at %q for backup from database %p at %q", opts.DestPath, db, db.Path)
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

	db.logger.Debugf("backup connection %p and %p from %q to %q established",
		srcConn, destConn, db.Path, opts.DestPath)

	for {
		// Surprisingly, this is the best way to avoid getting a "database locked" error,
		// instead of saving a few pages at a time.
		// This is probably due to SQLite's deadlock detetection in its notify API.
		db.logger.Debugf("backup connection %p and %p from %q to %q trying to backup all pages",
			srcConn, destConn, db.Path, opts.DestPath)
		err = backup.Step(-1) // -1 saves all remaining pages.
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
