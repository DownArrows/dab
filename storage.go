package main

import "time"

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
// It returns the connection it needed to run the checks; if you are using a temporary database, keep it open until shut down.
func NewStorage(ctx Ctx, logger LevelLogger, conf StorageConf) (*Storage, StorageConn, error) {
	conn := StorageConn{}
	db, baseConn, err := NewSQLiteDatabase(ctx, logger, SQLiteDatabaseOptions{
		AppID:           ApplicationFileID,
		CleanupInterval: conf.CleanupInterval.Value,
		Migrations:      StorageMigrations,
		Path:            conf.Path,
		Retry:           conf.Retry,
		Timeout:         conf.Timeout.Value,
		Version:         Version,
	})
	if err != nil {
		return nil, conn, err
	}
	conn.actual = baseConn

	kv, err := NewKeyValueStore(conn, "key_value")
	if err != nil {
		return nil, conn, err
	}

	s := &Storage{
		backupMaxAge: conf.BackupMaxAge.Value,
		backupPath:   conf.BackupPath,
		db:           db,
		kv:           kv,
		logger:       logger,
	}

	if err := s.initTables(conn); err != nil {
		return nil, conn, err
	}

	return s, conn, nil
}

func (s *Storage) initTables(conn SQLiteConn) error {
	var queries []SQLQuery
	queries = append(queries, User{}.InitializationQueries()...)
	queries = append(queries, Comment{}.InitializationQueries()...)
	if err := conn.MultiExec(queries); err != nil {
		return err
	}
	return nil
}

// KV returns a key-value store.
func (s *Storage) KV() *KeyValueStore {
	return s.kv
}

// PeriodicCleanupIsEnabled tells if the setting for PeriodCleanup allow to run it.
func (s *Storage) PeriodicCleanupIsEnabled() bool {
	return s.db.CleanupInterval > 0
}

// PeriodicCleanup is a Task that periodically cleans up and optimizes the underlying database.
func (s *Storage) PeriodicCleanup(ctx Ctx) error {
	return s.db.PeriodicCleanup(ctx)
}

// BackupPath returns the set path for backups.
func (s *Storage) BackupPath() string {
	return s.backupPath
}

// Backup performs a backup on the destination returned by BackupPath.
func (s *Storage) Backup(ctx Ctx, conn StorageConn) error {
	if older, err := FileOlderThan(s.BackupPath(), s.backupMaxAge); err != nil {
		return err
	} else if !older {
		s.logger.Debugf("in Storage %p on %s, database backup was not older than %v, nothing was done", s, s.backupMaxAge)
		return nil
	}
	return s.db.Backup(ctx, conn, SQLiteBackupOptions{
		DestName: "main",
		DestPath: s.BackupPath(),
		SrcName:  "main",
	})
}

// GetConn creates new connections to the associated database.
func (s *Storage) GetConn(ctx Ctx) (StorageConn, error) {
	conn, err := s.db.GetConn(ctx)
	return StorageConn{actual: conn}, err
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

// StorageConnPool is a simple pool with a limited size.
// Do not release more conns than the set size, there's no check,
// and it will not behave properly relatively to cancellation.
type StorageConnPool struct {
	actual SQLiteConnPool
}

// NewStorageConnPool creates a new StorageConnPool with a global context.
func NewStorageConnPool(ctx Ctx, size uint, cb func(Ctx) (StorageConn, error)) (StorageConnPool, error) {
	pool, err := NewSQLiteConnPool(ctx, size, func(ctx Ctx) (SQLiteConn, error) { return cb(ctx) })
	return StorageConnPool{actual: pool}, err
}

// Analyze wraps SQLiteConnPool.
func (pool StorageConnPool) Analyze(ctx Ctx, interval time.Duration) error {
	return pool.actual.Analyze(ctx, interval)
}

// Acquire wraps SQLiteConnPool.
func (pool StorageConnPool) Acquire(ctx Ctx) (StorageConn, error) {
	conn, err := pool.actual.Acquire(ctx)
	if err != nil {
		return StorageConn{}, err
	}
	return conn.(StorageConn), nil
}

// Release wraps SQLiteConnPool.
func (pool StorageConnPool) Release(conn StorageConn) {
	pool.actual.Release(conn)
}

// WithConn wraps SQLiteConnPool.
func (pool StorageConnPool) WithConn(ctx Ctx, cb func(StorageConn) error) error {
	return pool.actual.WithConn(ctx, func(conn SQLiteConn) error { return cb(conn.(StorageConn)) })
}

// Close wraps SQLiteConnPool.
func (pool StorageConnPool) Close() error {
	return pool.actual.Close()
}
