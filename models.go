package main

import (
	"strings"
	"time"
)

type Comment struct {
	Id         string  `json:"id" db:"id"`
	Author     string  `json:"author" db:"author"`
	Score      int64   `json:"score" db:"score"`
	Permalink  string  `json:"permalink" db:"permalink"`
	Sub        string  `json:"subreddit" db:"sub"`
	RawCreated float64 `json:"created_utc" db:"-"`
	Created    int64   `json:"-" db:"created"`
	Body       string  `json:"body" db:"body"`
}

func (c Comment) FinishDecoding() Comment {
	c.Created = int64(c.RawCreated)
	return c
}

func (c Comment) CreatedTime() time.Time {
	return time.Unix(c.Created, 0)
}

type User struct {
	Name      string `db:"name" json:"name"`
	Created   int64  `db:"created" json:"created"`
	NotFound  bool   `db:"not_found" json:"-"`
	Suspended bool   `db:"suspended" json:"suspended"`

	Added     int64  `db:"added" json:"-"`
	BatchSize uint   `db:"batch_size" json:"-"`
	Hidden    bool   `db:"hidden" json:"-"`
	Inactive  bool   `db:"inactive" json:"-"`
	New       bool   `db:"new" json:"-"`
	Position  string `db:"position" json:"-"`
}

func (u User) CreatedTime() time.Time {
	return time.Unix(u.Created, 0)
}

func (u User) AddedTime() time.Time {
	return time.Unix(u.Added, 0)
}

func (u *User) Username(username string) bool {
	return strings.ToLower(u.Name) == strings.ToLower(username)
}

type UserQuery struct {
	User   User
	Exists bool
	Error  error
}
