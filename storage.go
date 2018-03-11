package main

import (
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"io"
	"log"
	"sync"
	"time"
)

type Storage struct {
	sync.Mutex
	db     *sql.DB
	logger *log.Logger
	Path   string
}

func NewStorage(dbPath string, logOut io.Writer) (*Storage, error) {
	logger := log.New(logOut, "storage: ", log.LstdFlags)
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=1", dbPath))
	if err != nil {
		return nil, err
	}
	storage := &Storage{db: db, logger: logger, Path: dbPath}
	err = storage.Init()
	if err != nil {
		return nil, err
	}
	return storage, nil
}

func (storage *Storage) Init() error {
	if storage.Path != ":memory:" {
		err := storage.EnableWAL()
		if err != nil {
			return err
		}
	}

	_, err := storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS tracked (
			name TEXT PRIMARY KEY,
			created TIMESTAMP NOT NULL,
			suspended BOOLEAN DEFAULT 0 NOT NULL,
			deleted BOOLEAN DEFAULT 0 NOT NULL,
			added TIMESTAMP NOT NULL,
			hidden BOOLEAN NOT NULL,
			new BOOLEAN DEFAULT 1 NOT NULL,
			position TEXT DEFAULT "" NOT NULL
		) WITHOUT ROWID`)
	if err != nil {
		return err
	}

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
	if err != nil {
		return err
	}

	_, err = storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS seen_posts (
			id TEXT PRIMARY KEY,
			sub TEXT NOT NULL,
			created TIMESTAMP NOT NULL
		) WITHOUT ROWID`)
	if err != nil {
		return err
	}

	_, err = storage.db.Exec(`
		CREATE VIEW IF NOT EXISTS
			users(name, created, added, suspended, hidden, new, position)
		AS
			SELECT name, created, added, suspended, hidden, new, position
			FROM tracked WHERE deleted = 0
	`)
	return err
}

func (storage *Storage) EnableWAL() error {
	var journal_mode string

	err := storage.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal_mode)
	if err != nil {
		return err
	}

	if journal_mode != "wal" {
		return errors.New("Failed to set journal mode to Write-Ahead Log (WAL)")
	}

	return nil
}

func (storage *Storage) Vacuum() error {
	storage.Lock()
	defer storage.Unlock()

	_, err := storage.db.Exec("VACUUM")
	return err
}

/*****
 Users
******/

func (storage *Storage) AddUser(username string, hidden bool, created time.Time) error {
	storage.Lock()
	defer storage.Unlock()

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
		SELECT name, hidden, suspended, new, created, added, position
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

	err = rows.Scan(&query.User.Name, &query.User.Hidden, &query.User.Suspended,
		&query.User.New, &query.User.Created, &query.User.Added, &query.User.Position)
	if err != nil {
		query.Error = err
		return query
	}

	query.Exists = true
	return query
}

func (storage *Storage) DelUser(username string) error {
	storage.Lock()
	defer storage.Unlock()

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
		return errors.New("No user named " + username)
	}
	return nil
}

func (storage *Storage) PurgeUser(username string) error {
	storage.Lock()
	defer storage.Unlock()

	comments_stmt, err := storage.db.Prepare("DELETE FROM comments WHERE author = ? COLLATE NOCASE")
	if err != nil {
		return err
	}
	defer comments_stmt.Close()

	_, err = comments_stmt.Exec(username)
	if err != nil {
		return err
	}

	user_stmt, err := storage.db.Prepare("DELETE FROM tracked WHERE name = ? COLLATE NOCASE")
	if err != nil {
		return err
	}
	defer user_stmt.Close()

	result, err := user_stmt.Exec(username)
	if err != nil {
		return err
	}
	if nb, _ := result.RowsAffected(); nb == 0 {
		return errors.New("No user named " + username)
	}

	return nil
}

func (storage *Storage) ListUsers() ([]User, error) {
	rows, err := storage.db.Query(`
		SELECT name, hidden, new, created, added, position
		FROM users WHERE suspended = 0 ORDER BY name`)
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

func (storage *Storage) ListSuspended() ([]User, error) {
	rows, err := storage.db.Query(`
		SELECT name, hidden, new, created, added, position
		FROM tracked WHERE suspended = 1 AND deleted = 0
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
	storage.Lock()
	defer storage.Unlock()

	stmt, err := storage.db.Prepare("UPDATE tracked SET new = 0 WHERE name = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(username)
	return err
}

/********
 Comments
*********/

func (storage *Storage) ResetPosition(username string) error {
	storage.Lock()
	defer storage.Unlock()

	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}
	err = storage.savePosition(tx, username, "")
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Make sure the comments are all from the same user and its struct is up to date
func (storage *Storage) SaveCommentsPage(comments []Comment, user User) error {
	storage.Lock()
	defer storage.Unlock()

	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}

	err = storage.saveComments(tx, comments)
	if err != nil {
		return err
	}

	err = storage.savePosition(tx, user.Name, user.Position)
	if err != nil {
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
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO comments VALUES (?, ?, ?, ?, ?, ?, ?)")
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
		tx.Rollback()
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
		return 0, errors.New("No comments from user " + username + " found")
	}
	return karma.Int64, err
}

func (storage *Storage) StatsBetween(since, until time.Time) (Stats, error) {
	stmt, err := storage.db.Prepare(`
		SELECT
			comments.author AS author,
			AVG(comments.score) AS average,
			SUM(comments.score) AS delta,
			COUNT(comments.id) AS count
		FROM comments JOIN users
		ON comments.author = users.name
		WHERE comments.created BETWEEN ? AND ?
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

	stats := make(Stats)
	for rows.Next() {
		var data UserStats
		err = rows.Scan(&data.Author, &data.Average, &data.Delta, &data.Count)
		if err != nil {
			return nil, err
		}
		stats[data.Author] = data
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
	storage.Lock()
	defer storage.Unlock()

	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO seen_posts(id, sub, created) VALUES (?, ?, ?)")
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

		err = rows.Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}
