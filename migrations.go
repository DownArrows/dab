package main

import (
	"strconv"
)

// StorageMigrations defines the migrations for the whole application.
// Keep the migrations sorted from the lowest to highest version, the migration logic doesn't check.
// The connection given to the migrations is always the same one, and hasn't started a transaction.
var StorageMigrations = []SQLiteMigration{
	{
		From: SemVer{1, 10, 1},
		To:   SemVer{1, 11, 0},
		Exec: func(conn SQLiteConn) error {
			return conn.MultiExecWithTx([]SQLQuery{
				{SQL: `CREATE TABLE key_value (
					key TEXT NOT NULL,
					value TEXT NOT NULL,
					created INTEGER NOT NULL,
					PRIMARY KEY (key, value)
				) WITHOUT ROWID`},
				// Everything but highscores won't cause event flood if its key-value store is deleted.
				// Since we don't know where every ID comes from, just dump everything into highscores
				// that could be in it, so as to avoid flood in priority.
				{SQL: `INSERT INTO key_value(key, value, created)
					SELECT "highscores", id, date FROM known_objects
					WHERE NOT (id LIKE "submissions-from-%" OR id LIKE "username-%")`},
				{SQL: "DROP TABLE seen_posts"},
				{SQL: "DROP TABLE known_objects"},
			})
		},
	}, {
		From: SemVer{1, 11, 0},
		To:   SemVer{1, 12, 0},
		Exec: func(conn SQLiteConn) error {
			if err := conn.Exec("PRAGMA foreign_keys = OFF"); err != nil {
				return err
			}
			return conn.MultiExecWithTx([]SQLQuery{
				// The view "users" has a new column for compatibility with User.FromDB which needs all columns.
				{SQL: "DROP VIEW users"},
				// The new driver is sensitive to columns' order, so rebuild user_archive correctly.
				{SQL: "DROP INDEX user_archive_idx"},
				{SQL: `CREATE TABLE IF NOT EXISTS new_user_archive (
					name TEXT PRIMARY KEY,
					created INTEGER NOT NULL,
					not_found BOOLEAN DEFAULT FALSE NOT NULL,
					suspended BOOLEAN DEFAULT FALSE NOT NULL,
					added INTEGER NOT NULL,
					batch_size INTEGER DEFAULT ` + strconv.Itoa(MaxRedditListingLength) + ` NOT NULL,
					deleted BOOLEAN DEFAULT FALSE NOT NULL,
					hidden BOOLEAN NOT NULL,
					inactive BOOLEAN DEFAULT FALSE NOT NULL,
					last_scan INTEGER DEFAULT FALSE NOT NULL,
					new BOOLEAN DEFAULT TRUE NOT NULL,
					position TEXT DEFAULT "" NOT NULL
				) WITHOUT ROWID`},
				{SQL: `INSERT INTO new_user_archive SELECT
					name, created, not_found, suspended, added, batch_size, deleted, hidden, inactive, last_scan, new, position
				FROM user_archive`},
				{SQL: "DROP TABLE user_archive"},
				{SQL: "ALTER TABLE new_user_archive RENAME TO user_archive"},
				{SQL: "PRAGMA foreign_keys = ON"},
			})
		},
	}, {
		From: SemVer{1, 20, 1},
		To:   SemVer{1, 21, 0},
		Exec: func(conn SQLiteConn) error {
			return conn.MultiExecWithTx([]SQLQuery{
				{SQL: "DROP INDEX user_archive_idx"},
				{SQL: "DROP INDEX comments_stats_idx"},
			})
		},
	}, {
		From: SemVer{1, 22, 1},
		To:   SemVer{1, 22, 2},
		Exec: func(conn SQLiteConn) error {
			return conn.Exec(`UPDATE key_value
				SET key = "register-from-discord_" || REPLACE(key, "register-from-discord", "")
				WHERE key LIKE "register-from-discord%"`)
		},
	}, {
		From: SemVer{1, 22, 2},
		To:   SemVer{1, 22, 3},
		Exec: func(conn SQLiteConn) error {
			return conn.Exec(`UPDATE key_value
				SET key = "register-from-discord_" || REPLACE(key, "register-from-discord-", "")
				WHERE key LIKE "register-from-discord-%"`)
		},
	}, {
		From: SemVer{1, 25, 0},
		To:   SemVer{1, 26, 0},
		Exec: func(conn SQLiteConn) error {
			discordPrefix := "register-from-discord_"
			if err := conn.Exec("PRAGMA foreign_keys = OFF"); err != nil {
				return err
			}
			return conn.MultiExecWithTx([]SQLQuery{
				{SQL: "DROP VIEW users"},
				{SQL: "DROP INDEX user_archive_idx"},
				{SQL: `CREATE TABLE IF NOT EXISTS new_user_archive (
					name TEXT PRIMARY KEY,
					created INTEGER NOT NULL,
					not_found BOOLEAN DEFAULT FALSE NOT NULL,
					suspended BOOLEAN DEFAULT FALSE NOT NULL,
					added INTEGER NOT NULL,
					batch_size INTEGER DEFAULT ` + strconv.Itoa(MaxRedditListingLength) + ` NOT NULL,
					deleted BOOLEAN DEFAULT FALSE NOT NULL,
					hidden BOOLEAN NOT NULL,
					inactive BOOLEAN DEFAULT FALSE NOT NULL,
					last_scan INTEGER DEFAULT FALSE NOT NULL,
					new BOOLEAN DEFAULT TRUE NOT NULL,
					notes TEXT,
					position TEXT DEFAULT "" NOT NULL
				) WITHOUT ROWID`},
				{SQL: `INSERT INTO new_user_archive
					WITH notes AS
						(SELECT
							REPLACE(key, ?, "") AS name,
							json_object("registration",
								json_object("from",
									json_object("id", value, "type", "discord")
								)) AS notes
						FROM key_value
						WHERE key LIKE (? || "%"))
					SELECT
						user_archive.name, created, not_found, suspended, added, batch_size,
						deleted, hidden, inactive, last_scan, new, notes.notes, position
					FROM user_archive LEFT JOIN notes
					ON user_archive.name = notes.name`, Args: []interface{}{discordPrefix, discordPrefix}},
				{SQL: "DROP TABLE user_archive"},
				{SQL: "ALTER TABLE new_user_archive RENAME TO user_archive"},
				{SQL: "PRAGMA foreign_keys = ON"},
				{SQL: "DELETE FROM key_value WHERE key LIKE (? || '%')", Args: []interface{}{discordPrefix}},
			})
		},
	},
}
