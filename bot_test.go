package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

type MockScanner struct {
	Spy     interface{}
	Comment Comment
}

func (ms *MockScanner) UserComments(username string, position string) ([]Comment, string, error) {
	if username != ms.Comment.Author {
		return nil, position, errors.New("Test user not found")
	}

	comments := []Comment{ms.Comment}
	return comments, "t3_818dfe", nil
}

func (ms *MockScanner) AboutUser(username string) (bool, string, int64, error) {
	if username != ms.Comment.Author {
		return false, "", 0, errors.New("user " + username + " not found")
	}
	return true, username, time.Now().Unix(), nil
}

func (ms *MockScanner) SubPosts(sub string, position string) ([]Comment, string, error) {
	posts := make([]Comment, 1)
	return posts, "", nil
}

func TestScanner(t *testing.T) {
	storage, err := NewStorage(":memory:", os.Stdout)
	if err != nil {
		t.Error(err)
	}

	now := float64(time.Now().Unix())
	scanner := &MockScanner{
		Comment: Comment{
			Id:        "t3_ada238f",
			Author:    "whoever",
			Score:     -1000,
			Permalink: "/r/whatever/something/something",
			Sub:       "whatever",
			Created:   now,
			Body:      "erf, whatever",
		},
	}

	bot := NewBot(scanner, storage, os.Stdout, 24, 1)

	t.Run("AddUser", func(t *testing.T) {
		ok, err := bot.AddUser("whoever", false)
		if err != nil {
			t.Error(err)
		}
		if !ok {
			t.Error("Couldn't add test user")
		}
	})

	t.Run("Run", func(t *testing.T) {
		err := bot.ScanOnce()
		if err != nil {
			t.Error(err)
		}
	})
}
