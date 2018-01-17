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
	Sub       string  `json:"subreddit"`
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
			added DATETIME DEFAULT CURRENT_TIMESTAMP
		)`)

	return err
}

func (storage *Storage) AddUser(username string, hidden bool) error {
	storage.Lock()
	defer storage.Unlock()

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

func (storage *Storage) Vacuum() error {
	storage.Lock()
	defer storage.Unlock()

	_, err := storage.db.Exec("VACUUM")
	return err
}
