package main

import (
	"context"
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"testing"
)

func TestKeyValue(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", "file::memory:")
	if err != nil {
		t.Error(err)
		t.Fail()
		return
	}

	db.SetMaxOpenConns(1)

	kv, err := NewKeyValueStore(ctx, db, "test")
	if err != nil {
		t.Error(err)
		t.Fail()
		return
	}

	t.Run("write", func(t *testing.T) {
		if err := kv.Save(ctx, "key1", "value1"); err != nil {
			t.Error(err)
		}
	})

	t.Run("has key/value", func(t *testing.T) {
		if !kv.Has("key1", "value1") {
			t.Error("'key1/value1' should be in the store")
		}
	})

	t.Run("unknown value", func(t *testing.T) {
		if kv.Has("key1", "unknown") {
			t.Error("'unknown' should not be tested as present for the key 'key1'")
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		if kv.HasKey("unknown") {
			t.Error("'unknown' key should be present")
		}
	})

	t.Run("write many", func(t *testing.T) {
		if err := kv.SaveMany(ctx, "key1", []string{"value2", "value3"}); err != nil {
			t.Error(err)
			return
		}

		if !kv.Has("key1", "value2") {
			t.Error("'key1/value2' should be in the store")
		}

		if !kv.Has("key1", "value3") {
			t.Error("'key1/value3' should be in the store")
		}
	})

	t.Run("caching on startup", func(t *testing.T) {
		kv2, err := NewKeyValueStore(ctx, db, "test")
		if err != nil {
			t.Error(err)
			return
		}

		if !kv2.Has("key1", "value1") {
			t.Error("new key/value store using the same database connection and table name should cache 'key1/value1'")
		}
	})

	db.Close()
}
