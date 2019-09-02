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
func (s *Storage) PeriodicCleanup(ctx context.Context) error {
	return s.db.PeriodicCleanup(ctx)
}

// BackupPath returns the set path for backups.
func (s *Storage) BackupPath() string {
	return s.backupPath
}

// Backup performs a backup on the destination returned by BackupPath.
func (s *Storage) Backup(ctx context.Context, conn StorageConn) error {
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

// GetConn creates new connections to the associated database.
func (s *Storage) GetConn(ctx context.Context) (StorageConn, error) {
	conn, err := s.db.GetConn(ctx)
	return StorageConn{actual: conn}, err
}

// WithConn manages a connection's lifecycle.
func (s *Storage) WithConn(ctx context.Context, cb func(StorageConn) error) error {
	conn, err := s.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return cb(conn)
}

// MakePool creates a pool of connections.
func (s *Storage) MakePool(ctx context.Context, size uint) (StorageConnPool, error) {
	pool := NewStorageConnPool(ctx, size)
	for i := uint(0); i < size; i++ {
		conn, err := s.GetConn(ctx)
		if err != nil {
			pool.Close()
			return pool, err
		}
		pool.Release(conn)
	}
	return pool, nil
}

// StorageConnPool is a simple pool with a limited size.
// Do not release more conns than the set size, there's no check,
// and it will not behave properly relatively to cancellation.
type StorageConnPool struct {
	ch   chan StorageConn
	ctx  context.Context
	size uint
}

// NewStorageConnPool creates a new StorageConnPool with a global context.
func NewStorageConnPool(ctx context.Context, size uint) StorageConnPool {
	return StorageConnPool{
		ch:   make(chan StorageConn, size),
		ctx:  ctx,
		size: size,
	}
}

// Acquire gets a connection or waits until it is cancelled or receives a connection.
func (pool StorageConnPool) Acquire(ctx context.Context) (StorageConn, error) {
	var conn StorageConn
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
func (pool StorageConnPool) Release(conn StorageConn) {
	pool.ch <- conn
}

// WithConn automatically acquires and releases a connection.
func (pool StorageConnPool) WithConn(ctx context.Context, cb func(StorageConn) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer pool.Release(conn)
	return cb(conn)
}

// ForEach runs a function on every connection of the database, assuming nothing else is using the pool at the same time.
func (pool StorageConnPool) ForEach(ctx context.Context, cb func(StorageConn) error) error {
	for i := uint(0); i < pool.size; i++ {
		if err := pool.WithConn(ctx, cb); err != nil {
			return err
		}
	}
	return nil
}

// Close closes all connections; Release panics after this.
func (pool StorageConnPool) Close() error {
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
