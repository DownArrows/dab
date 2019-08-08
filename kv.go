package main

import (
	"context"
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"sync"
	"time"
)

// Key-Value store with in-memory reads and on-disk writes.
type KeyValueStore struct {
	sync.RWMutex
	db          *SQLiteDatabase
	insertQuery string
	store       map[string]map[string]struct{}
	table       string
}

func NewKeyValueStore(ctx context.Context, db *SQLiteDatabase, table string) (*KeyValueStore, error) {
	kv := &KeyValueStore{
		db:          db,
		insertQuery: fmt.Sprintf("INSERT INTO %s(key, value, created) VALUES (?, ?, ?)", table),
		store:       make(map[string]map[string]struct{}),
		table:       table,
	}

	if err := kv.init(ctx); err != nil {
		return nil, err
	}

	if err := kv.readAll(ctx); err != nil {
		return nil, err
	}

	return kv, nil
}

func (kv *KeyValueStore) init(ctx context.Context) error {
	sql := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			created INTEGER NOT NULL,
			PRIMARY KEY (key, value)
		) WITHOUT ROWID`, kv.table)
	return kv.db.Exec(ctx, sql)
}

func (kv *KeyValueStore) readAll(ctx context.Context) error {
	return kv.db.Select(ctx, "SELECT key, value FROM "+kv.table, func(stmt *sqlite.Stmt) error {
		key, _, err := stmt.ColumnText(0)
		if err != nil {
			return err
		}

		value, _, err := stmt.ColumnText(1)
		if err != nil {
			return err
		}

		if _, ok := kv.store[key]; !ok {
			kv.store[key] = make(map[string]struct{})
		}
		kv.store[key][value] = struct{}{}

		return nil
	})
}

func (kv *KeyValueStore) Save(ctx context.Context, key string, value string) error {
	return kv.SaveMany(ctx, key, []string{value})
}

func (kv *KeyValueStore) SaveMany(ctx context.Context, key string, values []string) error {
	new := make(map[string]struct{})

	err := kv.db.WithTx(ctx, func(conn *SQLiteConn) error {
		stmt, err := conn.Prepare(kv.insertQuery)
		if err != nil {
			return err
		}
		defer stmt.Close()

		kv.RLock()
		if _, has_key := kv.store[key]; !has_key {
			for _, value := range values {
				if _, has_value := kv.store[key][value]; !has_value {
					new[value] = struct{}{}
				}
			}
		} else {
			for _, value := range values {
				new[value] = struct{}{}
			}
		}
		kv.RUnlock()

		for value, _ := range new {
			if err := stmt.Exec(key, value, time.Now().Unix()); err != nil {
				return err
			}
			if err := stmt.ClearBindings(); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	kv.Lock()
	if _, ok := kv.store[key]; !ok {
		kv.store[key] = new
	} else {
		for value, _ := range new {
			kv.store[key][value] = struct{}{}
		}
	}
	kv.Unlock()

	return nil
}

func (kv *KeyValueStore) Has(key, value string) bool {
	kv.RLock()
	defer kv.RUnlock()
	if _, ok := kv.store[key]; !ok {
		return false
	}
	_, ok := kv.store[key][value]
	return ok
}

func (kv *KeyValueStore) HasKey(key string) bool {
	kv.RLock()
	_, ok := kv.store[key]
	kv.RUnlock()
	return ok
}

// TODO cleanup values older than T
