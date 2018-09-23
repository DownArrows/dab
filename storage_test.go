package main

import (
	"testing"
	"time"
)

func rnow() time.Time {
	return time.Now().Round(time.Second)
}

func TestCRUDUsers(t *testing.T) {

	s := NewStorage(StorageConf{Path: ":memory:"})
	name := "Someone"
	created := rnow().Unix()

	t.Run("add", func(t *testing.T) {
		if err := s.AddUser(name, false, created); err != nil {
			t.Error(err)
			t.Fail()
		}
	})

	t.Run("get", func(t *testing.T) {
		query := s.GetUser("someone")
		user := query.User
		if user.Name != name {
			t.Errorf("user is named '%s', not '%s'", name, user.Name)
		}
		if user.Created != user.Created {
			t.Errorf("user was created at %v, not at %v", created, user.Created)
		}
	})

	t.Run("list", func(t *testing.T) {
		users := s.ListUsers()
		if !(len(users) == 1 && users[0].Name == name && users[0].Created == created) {
			t.Errorf("listing should contain user '%s' created at '%v', not %v", name, created, users)
		}
	})

	t.Run("delete", func(t *testing.T) {
		err := s.DelUser(name)
		if err != nil {
			t.Error(err)
		}
		users := s.ListUsers()
		if len(users) != 0 {
			t.Errorf("listing %v should contain no user", users)
		}
	})
}

func TestCRUDComments(t *testing.T) {
	s := NewStorage(StorageConf{Path: ":memory:"})

	author1 := User{
		Name:    "Author1",
		Created: rnow().Add(-4 * time.Hour).Unix(),
	}
	if err := s.AddUser(author1.Name, false, author1.Created); err != nil {
		t.Error(err)
		t.Fail()
	}

	author2 := User{
		Name:    "Author2",
		Created: rnow().Add(-4 * time.Hour).Unix(),
	}
	if err := s.AddUser(author2.Name, false, author2.Created); err != nil {
		t.Error(err)
		t.Fail()
	}

	author3 := User{
		Name:    "Author3",
		Created: rnow().Add(-4 * time.Hour).Unix(),
	}
	if err := s.AddUser(author3.Name, false, author3.Created); err != nil {
		t.Error(err)
		t.Fail()
	}

	comments := []Comment{{
		Id:        "comment1",
		Author:    "Author1",
		Score:     -100,
		Permalink: "https://example.org/comment1",
		Sub:       "sub",
		Created:   rnow().Unix(),
		Body:      "This is the first test comment.",
	}, {
		Id:        "comment2",
		Author:    "Author2",
		Score:     -70,
		Permalink: "https://example.org/comment2",
		Sub:       "sub",
		Created:   rnow().Unix(),
		Body:      "This is the second test comment.",
	}, {
		Id:        "comment3",
		Author:    "Author1",
		Score:     75,
		Permalink: "https://example.org/comment3",
		Sub:       "sub",
		Created:   rnow().Unix(),
		Body:      "This is the third test comment.",
	}, {
		Id:        "comment4",
		Author:    "Author2",
		Score:     -140,
		Permalink: "https://example.org/comment4",
		Sub:       "sub",
		Created:   rnow().Add(-2 * time.Hour).Unix(),
		Body:      "This is the fourth test comment.",
	}, {
		Id:        "comment5",
		Author:    "Author3",
		Score:     -340,
		Permalink: "https://example.org/comment5",
		Sub:       "sub",
		Created:   rnow().Add(-2 * time.Hour).Unix(),
		Body:      "This is the fifth test comment.",
	}}

	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(time.Hour)

	// Save comments
	t.Run("save", func(t *testing.T) {
		if err := s.SaveCommentsPage([]Comment{comments[0], comments[2]}, author1); err != nil {
			t.Error(err)
			t.Fail()
		}
		if err := s.SaveCommentsPage([]Comment{comments[1], comments[3]}, author2); err != nil {
			t.Error(err)
			t.Fail()
		}
		if err := s.SaveCommentsPage([]Comment{comments[4]}, author3); err != nil {
			t.Error(err)
			t.Fail()
		}
	})

	t.Run("read", func(t *testing.T) {
		comments := s.GetCommentsBelowBetween(-50, start, end)
		if !(len(comments) == 2 && comments[0].Id == "comment1" && comments[1].Id == "comment2" &&
			comments[0].Author == author1.Name && comments[1].Author == author2.Name &&
			comments[0].Score < 0 && comments[1].Score < 0) {
			t.Errorf("should have got two comments, one from author1, one from comment2, with negative scores, not %v", comments)
		}
	})

	t.Run("statistics only include negative comments", func(t *testing.T) {
		stats := s.StatsBetween(start, end)
		if !(len(stats) == 2 && stats[author1.Name].Count == 1 && stats[author1.Name].Average == -100 &&
			stats[author2.Name].Average == -70 && stats[author2.Name].Count == 1) {
			t.Errorf("expected two users with average -70 and -100, not %v", stats)
		}
	})

	t.Run("update and read inactivity", func(t *testing.T) {
		if err := s.UpdateInactiveStatus(time.Hour); err != nil {
			t.Error(err)
			t.Fail()
		}

		active := s.ListActiveUsers()
		users := map[string]User{}
		for _, user := range active {
			users[user.Name] = user
		}

		_, ok1 := users[author1.Name]
		_, ok2 := users[author2.Name]
		if !(len(active) == 2 && ok1 && ok2) {
			t.Errorf("expected author1 and author2 to be active, not %v", active)
		}
	})
}
