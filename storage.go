package main

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"io"
	"log"
	"sync"
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
	Name       string
	Hidden     bool
	Deleted    bool
	EndReached bool
	Added      uint64
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
			username TEXT PRIMARY KEY,
			deleted BOOLEAN DEFAULT 0,
			added DATETIME DEFAULT CURRENT_TIMESTAMP,
			hidden BOOLEAN NOT NULL,
			new BOOLEAN DEFAULT 0,
			after TEXT DEFAULT "")`)

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
			FOREIGN KEY (author) REFERENCES tracked(username)
		) WITHOUT ROWID`)

	return err
}

func (storage *Storage) AddUser(username string, hidden bool) error {
	stmt, err := storage.db.Prepare("INSERT INTO tracked(username, hidden) VALUES (?, ?)")
	defer stmt.Close()
	if err != nil {
		return err
	}
	_, err = stmt.Exec(username, hidden)
	return err
}

func (storage *Storage) ListUsers() ([]string, error) {
	rows, err := storage.db.Query("SELECT username FROM tracked WHERE deleted = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]string, 0, 100)
	for rows.Next() {
		var username string
		err = rows.Scan(&username)
		if err != nil {
			return nil, err
		}
		users = append(users, username)
	}
	return users, nil
}

func (storage *Storage) DelUser(username string) error {
	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("UPDATE tracked SET deleted = 1 WHERE username = ?")
	defer stmt.Close()
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = stmt.Exec(username)
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (storage *Storage) SaveComment(comments ...Comment) error {
	tx, err := storage.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO comments VALUES (?, ?, ?, ?, ?, ?, ?)")
	defer stmt.Close()
	if err != nil {
		tx.Rollback()
		return err
	}
	for _, comment := range comments {
		_, err = stmt.Exec(comment.Id, comment.Author, comment.Score,
			comment.Permalink, comment.SubId, uint64(comment.Created),
			comment.Body)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
