package main

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyValue(t *testing.T) {
	t.Parallel()

	logger := NewTestLevelLogger(t)

	ctx := context.Background()

	dir, err := ioutil.TempDir("", "kv-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.db")

	db, err := NewSQLiteDatabase(ctx, logger, SQLiteDatabaseOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	kv, err := NewKeyValueStore(conn, "test")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("write", func(t *testing.T) {
		if err := kv.Save(conn, "key1", "value1"); err != nil {
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
		if err := kv.SaveMany(conn, "key1", []string{"value2", "value3"}); err != nil {
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
		kv2, err := NewKeyValueStore(conn, "test")
		if err != nil {
			t.Fatal(err)
		}

		if !kv2.Has("key1", "value1") {
			t.Error("new key/value store using the same database connection and table name should cache 'key1/value1'")
		}
	})

}
