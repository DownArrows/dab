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

func (comment Comment) FinishDecoding() Comment {
	comment.Created = int64(comment.RawCreated)
	return comment
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

func (user *User) Username(username string) bool {
	return strings.ToLower(user.Name) == strings.ToLower(username)
}

type UserQuery struct {
	User   User
	Exists bool
	Error  error
}
