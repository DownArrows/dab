package main

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteConn(t *testing.T) {
	t.Parallel()

	dir, err := ioutil.TempDir("", "dab-test-sqlite-conn")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.db")
	ctx := context.Background()
	connOpts := SQLiteConnOptions{
		Path:        path,
		Timeout:     SQLiteDefaultTimeout,
		OpenOptions: SQLiteDefaultOpenOptions,
	}
	expected := []string{"value1", "value2", "value3", "value4"}

	t.Run("create connection", func(t *testing.T) {
		if conn, err := NewBaseSQLiteConn(ctx, NewTestLevelLogger(t), connOpts); err != nil {
			t.Fatal(err)
		} else {
			conn.Close()
		}
	})

	t.Run("exec create table", func(t *testing.T) {
		conn, err := NewBaseSQLiteConn(ctx, NewTestLevelLogger(t), connOpts)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		if err := conn.Exec("CREATE TABLE test(value TEXT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("prepared statement insert", func(t *testing.T) {
		conn, err := NewBaseSQLiteConn(ctx, NewTestLevelLogger(t), connOpts)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		stmt, err := conn.Prepare("INSERT INTO test VALUES (?)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()

		for _, value := range expected {
			if err := stmt.Exec(value); err != nil {
				t.Fatal(err)
			}
			if err := stmt.ClearBindings(); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("select previously inserted values", func(t *testing.T) {
		conn, err := NewBaseSQLiteConn(ctx, NewTestLevelLogger(t), connOpts)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		var actual []string
		cb := func(stmt *SQLiteStmt) error {
			if value, ok, err := stmt.ColumnText(0); err != nil {
				return err
			} else if !ok {
				t.Errorf("column 'value' of table 'test' is NULL")
			} else {
				actual = append(actual, value)
			}
			return nil
		}
		if err := conn.Select("SELECT value FROM test", cb); err != nil {
			t.Fatal(err)
		}

		if !EqualStringSlices(actual, expected) {
			t.Errorf("table 'test' should contain %v, not %v", expected, actual)
		}
	})

	t.Run("multi exec", func(t *testing.T) {
		conn, err := NewBaseSQLiteConn(ctx, NewTestLevelLogger(t), connOpts)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		queries := []SQLQuery{
			{SQL: "CREATE TABLE test2(num INTEGER NOT NULL, txt TEXT NOT NULL)"},
			{SQL: "INSERT INTO test2 VALUES(?, ?)", Args: []interface{}{12, "test"}},
			{SQL: "DROP TABLE test2"},
		}
		if err := conn.MultiExec(queries); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSQLiteDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	appID := 0x1e51
	dir, err := ioutil.TempDir("", "dab-test-sqlite-db")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("open checks", func(t *testing.T) {
		path := filepath.Join(dir, "checks.db")

		t.Run("open empty", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 2, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("open already existing", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 2, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("open already existing database with lower version", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 3, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("open already existing database with higher version", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 1, 0},
			})
			if err == nil {
				t.Error("opening a database whose file has a higher version should fail")
			}
		})
	})

	t.Run("backups", func(t *testing.T) {
		path := filepath.Join(dir, "test-backup.db")
		backupPath := filepath.Join(dir, "test-backup.backup.db")

		db, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
			Path:    path,
			AppID:   appID,
			Version: SemVer{0, 3, 0},
		})

		conn, err := db.GetConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		if err := conn.Exec("CREATE TABLE test(id INTEGER NOT NULL)"); err != nil {
			t.Fatal(err)
		}

		opts := SQLiteBackupOptions{
			DestName: "main",
			DestPath: backupPath,
			SrcName:  "main",
		}
		if err := db.Backup(ctx, conn, opts); err != nil {
			t.Fatal(err)
		}

		backupDB, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
			Path:    backupPath,
			AppID:   appID,
			Version: SemVer{0, 3, 0},
		})
		if err != nil {
			t.Fatal(err)
		}

		backup, err := backupDB.GetConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer backup.Close()

		if err := backup.Exec("INSERT INTO test VALUES (?)", 1); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("delete temp directory", func(t *testing.T) {
		t.Helper()
		os.RemoveAll(dir)
	})
}

func EqualStringSlices(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for i, el := range first {
		if el != second[i] {
			return false
		}
	}
	return true
}
