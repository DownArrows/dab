package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// Key-Value store with in-memory reads and on-disk writes.
type KeyValueStore struct {
	sync.RWMutex
	db          *sql.DB
	insertQuery string
	store       map[string]map[string]struct{}
	table       string
}

func NewKeyValueStore(ctx context.Context, db *sql.DB, table string) (*KeyValueStore, error) {
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
	schema_query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			created INTEGER NOT NULL,
			PRIMARY KEY (key, value)
		) WITHOUT ROWID`, kv.table)
	_, err := kv.db.ExecContext(ctx, schema_query, kv.table)
	return err
}

func (kv *KeyValueStore) readAll(ctx context.Context) error {
	rows, err := kv.db.QueryContext(ctx, "SELECT key, value FROM "+kv.table)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	defer rows.Close()

	var key string
	var value string

	for rows.Next() {
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}

		if _, ok := kv.store[key]; !ok {
			kv.store[key] = make(map[string]struct{})
		}
		kv.store[key][value] = struct{}{}
	}

	return nil
}

func (kv *KeyValueStore) Save(ctx context.Context, key string, value string) error {
	return kv.SaveMany(ctx, key, []string{value})
}

func (kv *KeyValueStore) SaveMany(ctx context.Context, key string, values []string) error {
	tx, err := kv.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, kv.insertQuery)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	new := make(map[string]struct{})

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
		now := time.Now().Round(0).Unix()
		if _, err := stmt.ExecContext(ctx, key, value, now); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
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
