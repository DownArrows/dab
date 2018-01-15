package main

import (
	"database/sql"
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

type Comment struct {
	Id        string
	Author    string
	Score     int64
	Permalink string
	SubId     string  `json:"subreddit_id"`
	Created   float64 `json:"created_utc"`
	Body      string
}

type User struct {
	Name     string
	Hidden   bool
	New      bool
	Added    time.Time
	Position string
}

func NewStorage(db_path string, log_out io.Writer) (*Storage, error) {
	logger := log.New(log_out, "storage: ", log.LstdFlags)
	db, err := sql.Open("sqlite3", db_path)
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
			deleted BOOLEAN DEFAULT 0,
			added DATETIME DEFAULT CURRENT_TIMESTAMP,
			hidden BOOLEAN NOT NULL,
			new BOOLEAN DEFAULT 1,
			position TEXT DEFAULT "")`)

	if err != nil {
		return err
	}
	_, err = storage.db.Exec(`
		CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			author TEXT NOT NULL,
			score INTEGER NOT NULL,
			permalink TEXT NOT NULL,
			sub_id TEXT NOT NULL,
			created INTEGER NOT NULL,
			body TEXT NOT NULL,
			FOREIGN KEY (author) REFERENCES tracked(name)
		) WITHOUT ROWID`)

	return err
}

func (storage *Storage) AddUser(username string, hidden bool) error {
	stmt, err := storage.db.Prepare("INSERT INTO tracked(name, hidden) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(username, hidden)
	return err
}

func (storage *Storage) ListUsers() ([]User, error) {
	rows, err := storage.db.Query(`
		SELECT name, hidden, new, added, position
		FROM tracked
		WHERE deleted = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0, 100)
	for rows.Next() {
		var user User

		err = rows.Scan(&user.Name, &user.Hidden,
			&user.New, &user.Added, &user.Position)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (storage *Storage) DelUser(username string) error {
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

// Make sure the comments are all from the same author, it won't be checked
func (storage *Storage) SaveCommentsPage(comments []Comment, position string) error {
	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}

	username := comments[0].Author

	err = storage.saveComments(tx, username, comments)
	if err != nil {
		return err
	}

	err = storage.savePosition(tx, username, position)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (storage *Storage) saveComments(tx *sql.Tx, username string, comments []Comment) error {
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO comments VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, comment := range comments {
		_, err = stmt.Exec(comment.Id, comment.Author, comment.Score,
			comment.Permalink, comment.SubId, comment.Created, comment.Body)
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
