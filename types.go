package main

import "time"

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

type UserAddition struct {
	Name   string
	Hidden bool
	Exists bool
	Error  error
}

type RedditAuth struct {
	Username string
	Password string
	Id       string
	Key      string
}
