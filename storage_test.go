package main

import (
	"os"
	"reflect"
	"testing"
)

func TestCRUD(t *testing.T) {

	storage, err := NewStorage(":memory:", os.Stdout)
	if err != nil {
		t.Error(err)
	}

	t.Run("AddUser", func(t *testing.T) {
		storage.AddUser("whoever", false)
	})

	t.Run("ListUsers", func(t *testing.T) {
		expected := []string{"whoever"}
		users, err := storage.ListUsers()
		if err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(users, expected) {
			t.Error("expected:", expected, "actual:", users)
		}
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
			id: "t3_28df12",
			author: "whoever",
			score: -1039,
			permalink: "/r/something/something",
			sub_id: "t5_328fd1",
			created: 1515624337,
			body: "this is a test",
		}
		err := storage.SaveComment(comment)
		if err != nil {
			t.Error(err)
		}
	})
}
