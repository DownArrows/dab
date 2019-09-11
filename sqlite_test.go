package main

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
)

func TestSQLiteConn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	connOpts := SQLiteConnOptions{
		Path:        ":memory:",
		Timeout:     SQLiteDefaultTimeout,
		OpenOptions: SQLiteDefaultOpenOptions,
	}
	expected := []string{"value1", "value2", "value3", "value4"}

	var conn SQLiteConn

	t.Run("create connection", func(t *testing.T) {
		// Set the global connection and leave it open since we're using an in-memory database.
		var err error
		if conn, err = NewBaseSQLiteConn(ctx, NewTestLevelLogger(t), connOpts); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("exec create table", func(t *testing.T) {
		if err := conn.Exec("CREATE TABLE test(value TEXT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("prepared statement insert", func(t *testing.T) {
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
		queries := []SQLQuery{
			{SQL: "CREATE TABLE test2(num INTEGER NOT NULL, txt TEXT NOT NULL)"},
			{SQL: "INSERT INTO test2 VALUES(?, ?)", Args: []interface{}{12, "test"}},
			{SQL: "DROP TABLE test2"},
		}
		if err := conn.MultiExec(queries); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("close the connection", func(t *testing.T) {
		t.Helper()
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSQLiteDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	appID := 0x1e51
	path := ":memory:"

	t.Run("open checks", func(t *testing.T) {
		t.Parallel()

		file, err := ioutil.TempFile("", "dab_test_sqlite_conn")
		if err != nil {
			t.Fatal(err)
		}
		path := file.Name()
		file.Close()
		defer os.Remove(path)

		t.Run("open empty", func(t *testing.T) {
			_, conn, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 2, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		})

		t.Run("open already existing", func(t *testing.T) {
			_, conn, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 2, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		})

		t.Run("open already existing database with lower version", func(t *testing.T) {
			_, conn, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 3, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		})

		t.Run("open already existing database with higher version", func(t *testing.T) {
			_, conn, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   appID,
				Version: SemVer{0, 1, 0},
			})
			if err == nil {
				conn.Close()
				t.Fatal("opening a database whose file has a higher version should fail")
			}
		})
	})

	t.Run("backups", func(t *testing.T) {
		t.Parallel()

		db, conn, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		if err := conn.Exec("CREATE TABLE test(id INTEGER NOT NULL)"); err != nil {
			t.Fatal(err)
		}

		file, err := ioutil.TempFile("", "dab_test_sqlite_backup")
		if err != nil {
			t.Fatal(err)
		}
		backupPath := file.Name()
		file.Close()
		defer os.Remove(backupPath)

		err = db.Backup(ctx, conn, SQLiteBackupOptions{
			DestName: "main",
			DestPath: backupPath,
			SrcName:  "main",
		})
		if err != nil {
			t.Fatal(err)
		}

		_, backup, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{Path: backupPath})
		if err != nil {
			t.Fatal(err)
		}
		defer backup.Close()

		if err := backup.Exec("INSERT INTO test VALUES (?)", 1); err != nil {
			t.Fatal(err)
		}
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
