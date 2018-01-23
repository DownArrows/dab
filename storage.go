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
}

func NewStorage(db_path string, log_out io.Writer) (*Storage, error) {
	logger := log.New(log_out, "storage: ", log.LstdFlags)
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=1", db_path))
	if err != nil {
		return nil, err
	}
	storage := &Storage{db: db, logger: logger}
	err = storage.Init()
	if err != nil {
		return nil, err
	}
	return storage, nil
}

func (storage *Storage) Init() error {
	_, err := storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS tracked (
			name TEXT PRIMARY KEY,
			created DATETIME NOT NULL,
			deleted BOOLEAN DEFAULT 0 NOT NULL,
			added DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL,
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
			created INTEGER NOT NULL,
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
			created DATETIME NOT NULL
		) WITHOUT ROWID`)
	if err != nil {
		return err
	}

	_, err = storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS fortunes (
			id INTEGER PRIMARY KEY,
			content TEXT NOT NULL,
			added DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL
		)`)
	if err != nil {
		return err
	}

	_, err = storage.db.Exec(`
		CREATE VIEW IF NOT EXISTS
			users(name, created, added, hidden, new, position)
		AS
			SELECT name, created, added, hidden, new, position
			FROM tracked WHERE deleted = 0
	`)
	return err
}

func (storage *Storage) AddUser(username string, hidden bool, created int64) error {
	storage.Lock()
	defer storage.Unlock()

	stmt, err := storage.db.Prepare("INSERT INTO tracked(name, hidden, created) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(username, hidden, created)
	return err
}

func (storage *Storage) ListUsers() ([]User, error) {
	rows, err := storage.db.Query(`
		SELECT name, hidden, new, created, added, position
		FROM users ORDER BY name`)
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
	return users, nil
}

func (storage *Storage) GetUser(username string) UserQuery {
	query := UserQuery{User: User{Name: username}}

	stmt, err := storage.db.Prepare(`
		SELECT name, hidden, new, created, added, position
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

	err = rows.Scan(&query.User.Name, &query.User.Hidden, &query.User.New,
		&query.User.Created, &query.User.Added, &query.User.Position)
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

	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("UPDATE tracked SET deleted = 1 WHERE name = ?")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(username)
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (storage *Storage) Averages(since, until time.Time) (map[string]float64, error) {
	stmt, err := storage.db.Prepare(`
		SELECT comments.author, AVG(comments.score)
		FROM comments JOIN users
		ON comments.author = users.name
		WHERE comments.created BETWEEN ? AND ?
		GROUP BY comments.author
	`)
	if err != nil {
		return nil, err
	}

	rows, err := stmt.Query(since.Unix(), until.Unix())
	if err != nil {
		return nil, err
	}

	results := make(map[string]float64)
	for rows.Next() {
		var name string
		var average float64

		err = rows.Scan(&name, &average)
		if err != nil {
			return nil, err
		}
		results[name] = average
	}

	return results, nil
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
	return comments, nil
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

func (storage *Storage) saveComments(tx *sql.Tx, comments []Comment) error {
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO comments VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, comment := range comments {
		_, err = stmt.Exec(comment.Id, comment.Author, comment.Score,
			comment.Permalink, comment.Sub, comment.Created, comment.Body)
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
		_, err = stmt.Exec(post.Id, sub, int64(post.Created))
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
	return ids, nil
}

func (storage *Storage) SaveFortune(fortune string) error {
	storage.Lock()
	defer storage.Unlock()

	stmt, err := storage.db.Prepare("INSERT INTO fortunes(content) VALUES (?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(fortune)
	return err
}

func (storage *Storage) GetFortunes() ([]string, error) {
	rows, err := storage.db.Query("SELECT content FROM fortunes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fortunes := make([]string, 0, 10)
	for rows.Next() {
		var fortune string

		err = rows.Scan(&fortune)
		if err != nil {
			return nil, err
		}
		fortunes = append(fortunes, fortune)
	}
	return fortunes, nil
}

func (storage *Storage) GetTotalKarma(username string) (int64, error) {
	return storage.getKarma(username, "")
}

func (storage *Storage) GetNegativeKarma(username string) (int64, error) {
	return storage.getKarma(username, "score < 0 AND")
}

func (storage *Storage) LowestAverageBetween(since, until time.Time) (string, float64, uint64, error) {
	stmt, err := storage.db.Prepare(`
		SELECT author, MIN(avg_score), count
		FROM (
			SELECT comments.author AS author, AVG(comments.score) AS avg_score, COUNT(comments.id) AS count
			FROM comments JOIN users
			ON comments.author = users.name
			WHERE comments.created BETWEEN ? AND ?
			GROUP BY comments.author
		)`)
	if err != nil {
		return "", 0, 0, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(since.Unix(), until.Unix())
	if err != nil {
		return "", 0, 0, err
	}
	defer rows.Close()

	if !rows.Next() {
		return "", 0, 0, errors.New("No users registered")
	}

	var name string
	var average float64
	var count uint64

	err = rows.Scan(&name, &average, &count)
	return name, average, count, err
}

func (storage *Storage) LowestDeltaBetween(since, until time.Time) (string, int64, uint64, error) {
	stmt, err := storage.db.Prepare(`
		SELECT author, MIN(delta), count FROM (
			SELECT
				comments.author AS author,
				SUM(comments.score) AS delta,
				COUNT(comments.id) AS count
			FROM comments JOIN users
			ON comments.author = users.name
			WHERE comments.created BETWEEN ? AND ?
			GROUP BY comments.author
		)`)
	if err != nil {
		return "", 0, 0, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(since.Unix(), until.Unix())
	if err != nil {
		return "", 0, 0, err
	}
	defer rows.Close()

	var name string
	var delta int64
	var count uint64

	// Because of the join we're always having at least one column.
	// If there is no registered user, everything will be NULL,
	// and this function will return a cryptic error about "unsupported Scan",
	// but that's unto the user of this function to take care not to have an
	// empty database.
	// FIXME: if there is an ex æquo, only one of the result will be returned.
	rows.Next()
	err = rows.Scan(&name, &delta, &count)
	return name, delta, count, err
}

func (storage *Storage) getKarma(username, cond string) (int64, error) {
	query := fmt.Sprintf("SELECT sum(score) FROM comments WHERE %s author = ? COLLATE NOCASE", cond)
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

	var karma int64
	rows.Next()
	err = rows.Scan(&karma)
	return karma, err
}

func (storage *Storage) Vacuum() error {
	storage.Lock()
	defer storage.Unlock()

	_, err := storage.db.Exec("VACUUM")
	return err
}
