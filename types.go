package main

import (
	"strings"
	"time"
)

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
	Name      string
	Hidden    bool
	New       bool
	Suspended bool
	Created   time.Time
	Added     time.Time
	Position  string
}

func (user *User) Username(username string) bool {
	return strings.ToLower(user.Name) == strings.ToLower(username)
}

type UserQuery struct {
	User   User
	Exists bool
	Error  error
}

type RedditAuth struct {
	Username string
	Password string
	Id       string
	Key      string
}
