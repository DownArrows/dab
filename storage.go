package main

import "fmt"

// ApplicationFileID is the identification integer written in the SQLite file specific to the application.
const ApplicationFileID int = 0xdab

// Storage is a collection of methods to write, update, and retrieve all persistent data used throughout the application.
type Storage struct {
	StorageConf
	KV        *KeyValueStore
	SecretsKV *KeyValueStore
	db        *SQLiteDatabase
	logger    LevelLogger
	attach    []SQLQuery
}

// NewStorage returns a Storage instance after running initialization, checks, and migrations onto the target database file.
// It returns the connection it needed to run the checks; if you are using a temporary database, keep it open until shut down.
func NewStorage(ctx Ctx, logger LevelLogger, conf StorageConf) (*Storage, StorageConn, error) {
	var err error
	var conn StorageConn

	s := &Storage{
		StorageConf: conf,
		attach: []SQLQuery{
			{SQL: fmt.Sprintf("ATTACH DATABASE %q AS secrets", conf.SecretsPath)},
		},
		logger: logger,
	}

	s.db, conn.actual, err = NewSQLiteDatabase(ctx, logger, SQLiteDatabaseOptions{
		AppID:           ApplicationFileID,
		CleanupInterval: s.CleanupInterval.Value,
		Migrations:      StorageMigrations,
		Path:            s.Path,
		Retry:           s.Retry,
		Timeout:         s.Timeout.Value,
		Version:         Version,
	})
	if err != nil {
		return nil, conn, err
	}

	if err = s.initTables(conn); err != nil {
		return nil, conn, err
	}

	s.KV, err = NewKeyValueStore(conn, "key_value")
	if err != nil {
		return nil, conn, err
	}

	s.SecretsKV, err = NewKeyValueStore(conn, "secrets.key_value")
	if err != nil {
		return nil, conn, err
	}

	return s, conn, nil
}

func (s *Storage) initTables(conn SQLiteConn) error {
	var queries []SQLQuery
	queries = append(queries, s.attach...)
	queries = append(queries, User{}.InitializationQueries()...)
	queries = append(queries, Comment{}.InitializationQueries()...)
	queries = append(queries, CertCache{}.InitializationQueries()...)
	if err := conn.MultiExec(queries); err != nil {
		return err
	}
	return nil
}

// PeriodicCleanup is a Task that periodically cleans up and optimizes the underlying database.
func (s *Storage) PeriodicCleanup(ctx Ctx) error {
	return s.db.PeriodicCleanup(ctx)
}

// BackupMain performs a backup of the main database.
func (s *Storage) BackupMain(ctx Ctx, conn StorageConn) error {
	return s.backup(ctx, conn, s.Backup.Main, "main")
}

// BackupSecrets performs a backup of the database of secrets.
func (s *Storage) BackupSecrets(ctx Ctx, conn StorageConn) error {
	return s.backup(ctx, conn, s.Backup.Secrets, "secrets")
}

func (s *Storage) backup(ctx Ctx, conn StorageConn, path, name string) error {
	if older, err := FileOlderThan(path, s.Backup.MaxAge.Value); err != nil {
		return err
	} else if !older {
		s.logger.Debugd(func() interface{} {
			tmpl := "in Storage %p on %s, backup of database %q to %q was not older than %v, nothing was done"
			return fmt.Sprintf(tmpl, s, s.Path, name, path, s.Backup.MaxAge.Value)
		})
		return nil
	}
	return s.db.Backup(ctx, conn, SQLiteBackupOptions{
		DestName: "main",
		DestPath: path,
		SrcName:  name,
	})
}

// GetConn creates new connections to the associated database.
func (s *Storage) GetConn(ctx Ctx) (StorageConn, error) {
	var err error
	sc := StorageConn{}
	sc.actual, err = s.db.GetConn(ctx)
	if err != nil {
		return sc, err
	}
	err = sc.MultiExec(s.attach)
	if err != nil {
		return sc, err
	}
	return sc, err
}

// WithConn manages a connection's lifecycle.
func (s *Storage) WithConn(ctx Ctx, cb func(StorageConn) error) error {
	conn, err := s.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return cb(conn)
}
