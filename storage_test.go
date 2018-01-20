package main

import (
	"os"
	"testing"
)

func TestCRUD(t *testing.T) {

	storage, err := NewStorage(":memory:", os.Stdout)
	if err != nil {
		t.Error(err)
	}
	var whoever User

	t.Run("AddUser", func(t *testing.T) {
		storage.AddUser("whoever", false, int64(1516433169))
	})

	t.Run("ListUsers", func(t *testing.T) {
		users, err := storage.ListUsers()
		if err != nil {
			t.Error(err)
			return
		}
		for _, user := range users {
			if user.Name == "whoever" {
				whoever = user
				return
			}
		}
		t.Error("previously added user not found in", users)
	})

	t.Run("DelUser", func(t *testing.T) {
		err := storage.DelUser("whoever")
		if err != nil {
			t.Error(err)
		}

		users, err := storage.ListUsers()
		if err != nil {
			t.Error(err)
		}
		if len(users) != 0 {
			t.Error("lingering user(s):", users)
		}
	})

	t.Run("AddComments", func(t *testing.T) {
		comment := Comment{
			Id:        "t3_28df12",
			Author:    "whoever",
			Score:     -1039,
			Permalink: "/r/something/something",
			Sub:       "something",
			Created:   1515624337,
			Body:      "this is a test",
		}
		err := storage.SaveCommentsPage([]Comment{comment}, whoever)
		if err != nil {
			t.Error(err)
		}
	})
}
