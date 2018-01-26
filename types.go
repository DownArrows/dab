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
	Sub       string `json:"subreddit"`
	// This is only used for decoding JSON, otherwise user Created
	RawCreated float64   `json:"created_utc"`
	Created    time.Time `json:"-"` // This field exists in reddit's JSON with another type and meaning
	Body       string
}

func (comment Comment) FinishDecoding() Comment {
	comment.Created = time.Unix(int64(comment.RawCreated), 0)
	comment.RawCreated = 0
	return comment
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
