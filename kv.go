package main

import (
	"fmt"
	"sync"
	"time"
)

// KeyValueStore is a string-based key-value store with in-memory reads and on-disk writes that uses SQLite.
type KeyValueStore struct {
	sync.RWMutex
	insertQuery string
	store       map[string]map[string]struct{}
	table       string
}

// NewKeyValueStore creates a new KeyValueStore with the given SQLite database onto the given table, which it assumes has total control of.
func NewKeyValueStore(conn *SQLiteConn, table string) (*KeyValueStore, error) {
	kv := &KeyValueStore{
		insertQuery: fmt.Sprintf("INSERT INTO %s(key, value, created) VALUES (?, ?, ?)", table),
		store:       make(map[string]map[string]struct{}),
		table:       table,
	}

	if err := kv.init(conn); err != nil {
		return nil, err
	}

	if err := kv.readAll(conn); err != nil {
		return nil, err
	}

	return kv, nil
}

func (kv *KeyValueStore) init(conn *SQLiteConn) error {
	return conn.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			created INTEGER NOT NULL,
			PRIMARY KEY (key, value)
		) WITHOUT ROWID`, kv.table))
}

func (kv *KeyValueStore) readAll(conn *SQLiteConn) error {
	return conn.Select("SELECT key, value FROM "+kv.table, func(stmt *SQLiteStmt) error {
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

// Save saves a value associated to a key.
// Any number of values can be associated to a key.
func (kv *KeyValueStore) Save(conn *SQLiteConn, key string, value string) error {
	return kv.SaveMany(conn, key, []string{value})
}

// SaveMany saves several values associated with a single key.
func (kv *KeyValueStore) SaveMany(conn *SQLiteConn, key string, values []string) error {
	todo := make(map[string]struct{})

	err := conn.WithTx(func() error {
		stmt, err := conn.Prepare(kv.insertQuery)
		if err != nil {
			return err
		}
		defer stmt.Close()

		kv.RLock()
		if _, hasKey := kv.store[key]; !hasKey {
			for _, value := range values {
				if _, hasValue := kv.store[key][value]; !hasValue {
					todo[value] = struct{}{}
				}
			}
		} else {
			for _, value := range values {
				todo[value] = struct{}{}
			}
		}
		kv.RUnlock()

		for value := range todo {
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
		kv.store[key] = todo
	} else {
		for value := range todo {
			kv.store[key][value] = struct{}{}
		}
	}
	kv.Unlock()

	return nil
}

// Has returns whether the given key has the given value.
func (kv *KeyValueStore) Has(key, value string) bool {
	kv.RLock()
	defer kv.RUnlock()
	if _, ok := kv.store[key]; !ok {
		return false
	}
	_, ok := kv.store[key][value]
	return ok
}

// HasKey returns whether the given key exists.
func (kv *KeyValueStore) HasKey(key string) bool {
	kv.RLock()
	_, ok := kv.store[key]
	kv.RUnlock()
	return ok
}

// TODO cleanup values older than T
