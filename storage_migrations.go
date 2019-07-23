package main

type storageMigration struct {
	From SemVer
	To   SemVer
	Do   func(*Storage) error
}

// Keep the migrations sorted from the lowest to highest version, the migration logic doesn't check.
var storageMigrations = []storageMigration{
	{
		From: SemVer{1, 10, 1},
		To:   SemVer{1, 11, 0},
		Do: func(s *Storage) error {
			return s.RunQueryList([]string{
				`CREATE TABLE key_value (
					key TEXT NOT NULL,
					value TEXT NOT NULL,
					created INTEGER NOT NULL,
					PRIMARY KEY (key, value)
				) WITHOUT ROWID`,
				// Everything but highscores won't cause event flood if its key-value store is deleted.
				// Since we don't know where every ID comes from, just dump everything into highscores
				// that could be in it, so as to avoid flood in priority.
				`INSERT INTO key_value(key, value, created)
					SELECT "highscores", id, date FROM known_objects
					WHERE NOT (id LIKE "submissions-from-%" OR id LIKE "username-%")`,
				"DROP TABLE seen_posts",
				"DROP TABLE known_objects",
			})
		},
	},
}
