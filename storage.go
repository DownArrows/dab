package main

import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"time"
)

type Storage struct {
	db              *sql.DB
	Path            string
	CleanupInterval time.Duration
}

func NewStorage(conf StorageConf) *Storage {
	storage := &Storage{
		Path:            conf.Path,
		CleanupInterval: conf.CleanupInterval.Value,
	}
	if storage.CleanupInterval < 1*time.Minute && storage.CleanupInterval != 0*time.Second {
		panic("database cleanup interval can't be under a minute if superior to 0s")
	}

	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=1&cache=shared", storage.Path))
	autopanic(err)

	storage.db = db

	storage.Init()
	storage.launchPeriodicVacuum()

	return storage
}

func (storage *Storage) Init() {
	storage.db.SetMaxOpenConns(1)

	if storage.Path != ":memory:" {
		storage.EnableWAL()
	}

	_, err := storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS tracked (
			name TEXT PRIMARY KEY,
			created TIMESTAMP NOT NULL,
			inactive BOOLEAN DEFAULT 0 NOT NULL,
			suspended BOOLEAN DEFAULT 0 NOT NULL,
			deleted BOOLEAN DEFAULT 0 NOT NULL,
			added TIMESTAMP NOT NULL,
			hidden BOOLEAN NOT NULL,
			new BOOLEAN DEFAULT 1 NOT NULL,
			position TEXT DEFAULT "" NOT NULL
		) WITHOUT ROWID`)
	autopanic(err)

	_, err = storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			author TEXT NOT NULL,
			score INTEGER NOT NULL,
			permalink TEXT NOT NULL,
			sub TEXT NOT NULL,
			created TIMESTAMP NOT NULL,
			body TEXT NOT NULL,
			FOREIGN KEY (author) REFERENCES tracked(name)
		) WITHOUT ROWID`)
	autopanic(err)

	_, err = storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS seen_posts (
			id TEXT PRIMARY KEY,
			sub TEXT NOT NULL,
			created TIMESTAMP NOT NULL
		) WITHOUT ROWID`)
	autopanic(err)

	_, err = storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS known_objects (
			id TEXT PRIMARY KEY,
			date TIMESTAMP NOT NULL
		) WITHOUT ROWID`)
	autopanic(err)

	_, err = storage.db.Exec(`
		CREATE VIEW IF NOT EXISTS
			users(name, created, added, suspended, hidden, new, position, inactive)
		AS
			SELECT name, created, added, suspended, hidden, new, position, inactive
			FROM tracked WHERE deleted = 0
	`)
	autopanic(err)
}

func (storage *Storage) launchPeriodicVacuum() {
	if storage.CleanupInterval == 0*time.Second {
		return
	}

	go func() {
		for {
			time.Sleep(storage.CleanupInterval)
			autopanic(storage.Vacuum())
		}
	}()
}

func (storage *Storage) Close() {
	autopanic(storage.db.Close())
}

func (storage *Storage) EnableWAL() {
	var journal_mode string

	autopanic(storage.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal_mode))

	if journal_mode != "wal" {
		autopanic(fmt.Errorf("failed to set journal mode to Write-Ahead Log (WAL)"))
	}
}

func (storage *Storage) Vacuum() error {
	_, err := storage.db.Exec("VACUUM")
	return err
}

/*****
 Users
******/

func (storage *Storage) AddUser(username string, hidden bool, created time.Time) error {
	stmt, err := storage.db.Prepare(`
		INSERT INTO tracked(name, hidden, created, added)
		VALUES (?, ?, ?, strftime("%s", CURRENT_TIMESTAMP))`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(username, hidden, created.Unix())
	return err
}

func (storage *Storage) GetUser(username string) UserQuery {
	query := UserQuery{User: User{Name: username}}

	stmt, err := storage.db.Prepare(`
		SELECT name, hidden, suspended, new, created, added, position, inactive
		FROM users WHERE name = ? COLLATE NOCASE`)
	if err != nil {
		query.Error = err
		return query
	}
	defer stmt.Close()

	rows, err := stmt.Query(username)
	if err != nil {
		query.Error = err
		return query
	}
	defer rows.Close()

	if !rows.Next() {
		return query
	}
	if err = rows.Err(); err != nil {
		query.Error = err
		return query
	}

	err = rows.Scan(&query.User.Name, &query.User.Hidden, &query.User.Suspended, &query.User.New,
		&query.User.Created, &query.User.Added, &query.User.Position, &query.User.Inactive)
	if err != nil {
		query.Error = err
		return query
	}

	query.Exists = true
	return query
}

func (storage *Storage) DelUser(username string) error {
	stmt, err := storage.db.Prepare("UPDATE tracked SET deleted = 1 WHERE name = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	result, err := stmt.Exec(username)
	if err != nil {
		return err
	}
	if nb, _ := result.RowsAffected(); nb == 0 {
		return fmt.Errorf("no user named '%s'", username)
	}
	return nil
}

func (storage *Storage) PurgeUser(username string) error {
	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}

	comments_stmt, err := tx.Prepare("DELETE FROM comments WHERE author = ? COLLATE NOCASE")
	if err != nil {
		return err
	}
	defer comments_stmt.Close()

	_, err = comments_stmt.Exec(username)
	if err != nil {
		tx.Rollback()
		return err
	}

	user_stmt, err := tx.Prepare("DELETE FROM tracked WHERE name = ? COLLATE NOCASE")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer user_stmt.Close()

	result, err := user_stmt.Exec(username)
	if err != nil {
		tx.Rollback()
		return err
	}
	if nb, _ := result.RowsAffected(); nb == 0 {
		tx.Rollback()
		return fmt.Errorf("no user named '%s'", username)
	}

	return tx.Commit()
}

func (storage *Storage) EditHideUser(username string, hidden bool) error {
	stmt, err := storage.db.Prepare("UPDATE tracked SET hidden = ? WHERE name = ? COLLATE NOCASE")
	if err != nil {
		return err
	}

	result, err := stmt.Exec(hidden, username)
	if err != nil {
		return err
	}
	if nb, _ := result.RowsAffected(); nb == 0 {
		return fmt.Errorf("no user named '%s'", username)
	}
	return nil
}

func (storage *Storage) ListUsers() ([]User, error) {
	rows, err := storage.db.Query(`
		SELECT name, hidden, new, created, added, position, inactive
		FROM users WHERE suspended = 0 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0, 100)
	for rows.Next() {
		var user User

		err = rows.Scan(&user.Name, &user.Hidden, &user.New,
			&user.Created, &user.Added, &user.Position, &user.Inactive)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

func (storage *Storage) ListSuspended() ([]User, error) {
	rows, err := storage.db.Query(`
		SELECT name, hidden, new, created, added, position, inactive
		FROM users WHERE suspended = 1
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0, 100)
	for rows.Next() {
		var user User

		err = rows.Scan(&user.Name, &user.Hidden, &user.New,
			&user.Created, &user.Added, &user.Position, &user.Inactive)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

func (storage *Storage) SetSuspended(username string, suspended bool) error {
	stmt, err := storage.db.Prepare("UPDATE tracked SET suspended = ? WHERE name = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(suspended, username)
	return err
}

func (storage *Storage) NotNewUser(username string) error {
	stmt, err := storage.db.Prepare("UPDATE tracked SET new = 0 WHERE name = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(username)
	return err
}

func (storage *Storage) ListActiveUsers() ([]User, error) {
	rows, err := storage.db.Query(`
		SELECT name, hidden, new, created, added, position
		FROM users WHERE inactive = 0 AND suspended = 0
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0, 100)
	for rows.Next() {
		var user User

		err = rows.Scan(&user.Name, &user.Hidden, &user.New,
			&user.Created, &user.Added, &user.Position)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

func (storage *Storage) UpdateInactiveStatus(max_age time.Duration) error {
	// We use two SQL statements instead of one because SQLite is too limited
	// to do that in a single statement that isn't exceedingly complicated.
	template := `
		UPDATE tracked SET inactive = ?
		WHERE name IN (
			SELECT author FROM (
				SELECT author, max(created) AS last
				FROM comments GROUP BY author
			) WHERE (? - last) %s ?
		)`

	now := time.Now().Round(0).Unix()

	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}

	inactive_stmt, err := tx.Prepare(fmt.Sprintf(template, ">"))
	if err != nil {
		tx.Rollback()
		return err
	}
	defer inactive_stmt.Close()

	_, err = inactive_stmt.Exec(1, now, max_age.Seconds())
	if err != nil {
		tx.Rollback()
		return err
	}

	active_stmt, err := tx.Prepare(fmt.Sprintf(template, "<"))
	if err != nil {
		tx.Rollback()
		return err
	}
	defer active_stmt.Close()

	_, err = active_stmt.Exec(0, now, max_age.Seconds())
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

/********
 Comments
*********/

func (storage *Storage) ResetPosition(username string) error {
	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}
	if err := storage.savePosition(tx, username, ""); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Make sure the comments are all from the same user and its struct is up to date
func (storage *Storage) SaveCommentsPage(comments []Comment, user User) error {
	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}

	if err := storage.saveComments(tx, comments); err != nil {
		tx.Rollback()
		return err
	}

	if err := storage.savePosition(tx, user.Name, user.Position); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (storage *Storage) GetCommentsBelowBetween(score int64, since, until time.Time) ([]Comment, error) {
	stmt, err := storage.db.Prepare(`
		SELECT
			comments.id, comments.author, comments.score, comments.sub,
			comments.permalink, comments.body, comments.created
		FROM comments JOIN users
		ON comments.author = users.name
		WHERE
			comments.score <= ?
			AND users.hidden = 0
			AND comments.created BETWEEN ? AND ?
		ORDER BY comments.score ASC`)
	if err != nil {
		return nil, err
	}

	rows, err := stmt.Query(score, since.Unix(), until.Unix())
	if err != nil {
		return nil, err
	}

	return storage.scanComments(rows)
}

func (storage *Storage) scanComments(rows *sql.Rows) ([]Comment, error) {
	defer rows.Close()
	comments := make([]Comment, 0, 100)
	for rows.Next() {
		var comment Comment

		err := rows.Scan(&comment.Id, &comment.Author, &comment.Score,
			&comment.Sub, &comment.Permalink, &comment.Body, &comment.Created)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}

func (storage *Storage) saveComments(tx *sql.Tx, comments []Comment) error {
	stmt, err := tx.Prepare(`
		INSERT INTO comments VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			score=excluded.score,
			body=excluded.body
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, comment := range comments {
		_, err = stmt.Exec(comment.Id, comment.Author, comment.Score,
			comment.Permalink, comment.Sub, comment.Created.Unix(), comment.Body)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return nil
}

func (storage *Storage) savePosition(tx *sql.Tx, username string, position string) error {
	stmt, err := tx.Prepare("UPDATE tracked SET position = ? WHERE name = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(position, username)
	if err != nil {
		return err
	}

	return nil
}

/**********
 Statistics
***********/

func (storage *Storage) GetTotalKarma(username string) (int64, error) {
	return storage.getKarma(username, "")
}

func (storage *Storage) GetNegativeKarma(username string) (int64, error) {
	return storage.getKarma(username, "score < 0 AND")
}

func (storage *Storage) getKarma(username, cond string) (int64, error) {
	query := fmt.Sprintf("SELECT SUM(score) FROM comments WHERE %s author = ? COLLATE NOCASE", cond)
	stmt, err := storage.db.Prepare(query)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(username)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	rows.Next()
	if err = rows.Err(); err != nil {
		return 0, err
	}

	var karma sql.NullInt64
	err = rows.Scan(&karma)
	if !karma.Valid {
		return 0, fmt.Errorf("no comments from user '%s' found", username)
	}
	return karma.Int64, err
}

func (storage *Storage) StatsBetween(since, until time.Time) (UserStatsMap, error) {
	stmt, err := storage.db.Prepare(`
		SELECT
			comments.author AS author,
			AVG(comments.score) AS average,
			SUM(comments.score) AS delta,
			COUNT(comments.id) AS count
		FROM comments JOIN users
		ON comments.author = users.name
		WHERE
			score < 0
			AND users.hidden = 0
			AND comments.created BETWEEN ? AND ?
		GROUP BY comments.author`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(since.Unix(), until.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats = UserStatsMap{}
	for rows.Next() {
		var data UserStats
		if err := rows.Scan(&data.Name, &data.Average, &data.Delta, &data.Count); err != nil {
			return nil, err
		}
		stats[data.Name] = data
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return stats, nil
}

/*****
 Posts
******/

func (storage *Storage) SaveSubPostIDs(listing []Comment, sub string) error {
	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO seen_posts(id, sub, created) VALUES (?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, post := range listing {
		_, err = stmt.Exec(post.Id, sub, post.Created.Unix())
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func (storage *Storage) SeenPostIDs(sub string) ([]string, error) {
	stmt, err := storage.db.Prepare("SELECT id FROM seen_posts WHERE sub = ?")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(sub)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0, 100)
	for rows.Next() {
		var id string

		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}

func (storage *Storage) SaveKnownObject(id string) error {
	stmt, err := storage.db.Prepare("INSERT INTO known_objects VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(id, time.Now())
	return err
}

func (storage *Storage) IsKnownObject(id string) (bool, error) {
	stmt, err := storage.db.Prepare("SELECT id FROM known_objects WHERE id = ?")
	if err != nil {
		return false, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(id)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	if rows.Next() {
		return true, nil
	}

	if err = rows.Err(); err != nil {
		return false, err
	}

	return false, nil
}
