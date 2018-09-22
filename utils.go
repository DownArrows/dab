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
	Inactive  bool
}

func (user *User) Username(username string) bool {
	return strings.ToLower(user.Name) == strings.ToLower(username)
}

type UserQuery struct {
	User   User
	Exists bool
	Error  error
}
