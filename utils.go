package main

import (
	"fmt"
	"strings"
	"time"
)

// Simple utility functions

func autopanic(err error) {
	if err != nil {
		panic(err)
	}
}

type Chunks interface {
	Next(int) (string, int)
}

func Batches(chunks Chunks) ([][]string, error) {
	var batches [][]string

	var batch = []string{}
	len_batch := 0
	for {
		chunk, limit := chunks.Next(len(batches))
		if limit == 0 {
			break
		}

		len_chunk := len(chunk)

		if len_chunk > limit {
			return batches, fmt.Errorf("chunk '%s' is too long (%d > %d)", chunk, len_chunk, limit)
		}

		if len_batch+len_chunk > limit {
			batches = append(batches, batch)
			batch = []string{}
			len_batch = 0
		}

		batch = append(batch, chunk)
		len_batch += len(chunk)
	}

	if len(batch) > 0 {
		batches = append(batches, batch)
	}

	return batches, nil
}

// Common models

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
	Name      string
	Hidden    bool
	New       bool
	Suspended bool
	Created   int64
	Added     int64
	Position  string
	Inactive  bool
}

func (u User) CreatedTime() time.Time {
	return time.Unix(u.Created, 0)
}

func (u User) AddedTime() time.Time {
	return time.Unix(u.Created, 0)
}

func (user *User) Username(username string) bool {
	return strings.ToLower(user.Name) == strings.ToLower(username)
}

type UserQuery struct {
	User   User
	Exists bool
	Error  error
}
