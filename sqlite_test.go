package main

import (
	"context"
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSQLiteConn(t *testing.T) {
	t.Parallel()

	dir, err := ioutil.TempDir("", "sqlite-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.db")
	ctx := context.Background()
	conn_opts := SQLiteConnOptions{
		Path:        path,
		Timeout:     SQLiteDefaultTimeout,
		OpenOptions: SQLiteDefaultOpenOptions,
	}
	expected := []string{"value1", "value2", "value3", "value4"}

	t.Run("create connection", func(t *testing.T) {
		if conn, err := NewSQLiteConn(ctx, NewTestLevelLogger(t), conn_opts); err != nil {
			t.Fatal(err)
		} else {
			conn.Close()
		}
	})

	t.Run("exec create table", func(t *testing.T) {
		conn, err := NewSQLiteConn(ctx, NewTestLevelLogger(t), conn_opts)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		if err := conn.Exec("CREATE TABLE test(value TEXT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("prepared statement insert", func(t *testing.T) {
		conn, err := NewSQLiteConn(ctx, NewTestLevelLogger(t), conn_opts)
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
		conn, err := NewSQLiteConn(ctx, NewTestLevelLogger(t), conn_opts)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		var actual []string
		cb := func(stmt *sqlite.Stmt) error {
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
		conn, err := NewSQLiteConn(ctx, NewTestLevelLogger(t), conn_opts)
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

	app_id := 0x1e51
	dir, err := ioutil.TempDir("", "sqlite-test")
	if err != nil {
		t.Fatal(err)
	}

	parallel := &sync.WaitGroup{}

	parallel.Add(1)
	t.Run("open checks", func(t *testing.T) {
		t.Parallel()
		defer parallel.Done()

		path := filepath.Join(dir, "checks.db")

		t.Run("open empty", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   app_id,
				Version: SemVer{0, 2, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("open already existing", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   app_id,
				Version: SemVer{0, 2, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("open already existing database with lower version", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   app_id,
				Version: SemVer{0, 3, 0},
			})
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("open already existing database with higher version", func(t *testing.T) {
			_, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
				Path:    path,
				AppID:   app_id,
				Version: SemVer{0, 1, 0},
			})
			if err == nil {
				t.Error("opening a database whose file has a higher version should fail")
			}
		})
	})

	parallel.Add(1)
	t.Run("concurrent insert", func(t *testing.T) {
		t.Parallel()
		defer parallel.Done()

		nb_concurrent := 50

		db, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
			Path:    filepath.Join(dir, "concurrent-insert.db"),
			AppID:   app_id,
			Version: SemVer{0, 3, 0},
		})
		if err != nil {
			t.Fatal(err)
		}

		if err := db.Exec(ctx, "CREATE TABLE test(id INTEGER NOT NULL)"); err != nil {
			t.Fatal(err)
		}

		wg := &sync.WaitGroup{}
		for i := 0; i < nb_concurrent; i++ {
			wg.Add(1)
			value := i
			go func() {
				defer wg.Done()
				if err := db.Exec(ctx, "INSERT INTO test VALUES(?)", value); err != nil {
					t.Fatal(err)
				}
			}()
		}
		wg.Wait()

		cb := func(stmt *sqlite.Stmt) error {
			count, _, err := stmt.ColumnInt(0)
			if err != nil {
				return err
			}

			sum, _, err := stmt.ColumnInt(1)
			if err != nil {
				return err
			}

			if count != nb_concurrent {
				return fmt.Errorf("table 'test' should contain %d records, not %d", nb_concurrent, count)
			}

			if n := nb_concurrent - 1; sum != (n*(n+1))/2 {
				return fmt.Errorf("table 'test' should contain a list of all integers between 0 and %d", nb_concurrent)
			}

			return nil
		}
		if err := db.Select(ctx, "SELECT COUNT(id), SUM(id) FROM test", cb); err != nil {
			t.Fatal(err)
		}
	})

	parallel.Add(1)
	t.Run("backups", func(t *testing.T) {
		t.Parallel()
		defer parallel.Done()

		path := filepath.Join(dir, "test-backup.db")
		backup_path := filepath.Join(dir, "test-backup.backup.db")

		db, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
			Path:    path,
			AppID:   app_id,
			Version: SemVer{0, 3, 0},
		})

		if err := db.Exec(ctx, "CREATE TABLE test(id INTEGER NOT NULL)"); err != nil {
			t.Fatal(err)
		}

		opts := SQLiteBackupOptions{
			DestName: "main",
			DestPath: backup_path,
			SrcName:  "main",
		}
		if err := db.Backup(ctx, opts); err != nil {
			t.Fatal(err)
		}

		backup, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
			Path:    backup_path,
			AppID:   app_id,
			Version: SemVer{0, 3, 0},
		})
		if err != nil {
			t.Fatal(err)
		}

		if err := backup.Exec(ctx, "INSERT INTO test VALUES (?)", 1); err != nil {
			t.Fatal(err)
		}
	})

	parallel.Add(1)
	t.Run("cancellation", func(t *testing.T) {
		t.Parallel()
		defer parallel.Done()

		nb_concurrent := 50

		ctx, cancel := context.WithCancel(context.Background())

		db, err := NewSQLiteDatabase(ctx, NewTestLevelLogger(t), SQLiteDatabaseOptions{
			Path:    filepath.Join(dir, "cancellation.db"),
			AppID:   app_id,
			Version: SemVer{0, 3, 0},
		})
		if err != nil {
			t.Fatal(err)
		}

		if err := db.Exec(ctx, "CREATE TABLE test(id INTEGER NOT NULL)"); err != nil {
			t.Fatal(err)
		}

		wg := &sync.WaitGroup{}
		for i := 0; i < nb_concurrent; i++ {
			wg.Add(1)
			value := i
			go func() {
				defer wg.Done()
				if err := db.Exec(ctx, "INSERT INTO test VALUES(?)", value); err != nil && !IsCancellation(err) {
					t.Log(err)
				}
			}()
		}

		cancel()
		wg.Wait()
	})

	t.Run("delete temp directory", func(t *testing.T) {
		t.Helper()
		t.Parallel()
		parallel.Wait()
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
