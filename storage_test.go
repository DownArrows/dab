package main

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCRUDUsers(t *testing.T) {
	t.Parallel()

	dir, err := ioutil.TempDir("", "storage-users-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()

	s, err := NewStorage(ctx, NewTestLevelLogger(t), StorageConf{Path: path})
	if err != nil {
		t.Fatal(err)
	}

	name := "Someone"
	created := time.Now().Round(time.Second)

	t.Run("add", func(t *testing.T) {
		if err := s.AddUser(ctx, name, false, created); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("get", func(t *testing.T) {
		query := s.GetUser(ctx, "someone")
		if query.Error != nil {
			t.Fatal(query.Error)
		}

		user := query.User
		if user.Name != name {
			t.Errorf("user is named '%s', not '%s'", name, user.Name)
		}
		if user.Created != user.Created {
			t.Errorf("user was created at %v, not at %v", created, user.Created)
		}
	})

	t.Run("list", func(t *testing.T) {
		users, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if !(len(users) == 1 && users[0].Name == name && users[0].Created == created) {
			t.Errorf("listing should contain user '%s' created at '%v', not %v", name, created, users)
		}
	})

	t.Run("delete", func(t *testing.T) {
		err := s.DelUser(ctx, name)
		if err != nil {
			t.Fatal(err)
		}

		users, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if len(users) != 0 {
			t.Errorf("listing %v should contain no user", users)
		}
	})
}

func TestCRUDComments(t *testing.T) {

	dir, err := ioutil.TempDir("", "storage-comments-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()

	s, err := NewStorage(ctx, NewTestLevelLogger(t), StorageConf{Path: path})
	if err != nil {
		t.Fatal(err)
	}

	author1 := User{
		Name:    "Author1",
		Created: time.Now().Round(time.Second).Add(-4 * time.Hour),
	}
	if err := s.AddUser(ctx, author1.Name, false, author1.Created); err != nil {
		t.Fatal(err)
	}

	author2 := User{
		Name:    "Author2",
		Created: time.Now().Round(time.Second).Add(-4 * time.Hour),
	}
	if err := s.AddUser(ctx, author2.Name, false, author2.Created); err != nil {
		t.Fatal(err)
	}

	author3 := User{
		Name:    "Author3",
		Created: time.Now().Round(time.Second).Add(-4 * time.Hour),
	}
	if err := s.AddUser(ctx, author3.Name, false, author3.Created); err != nil {
		t.Fatal(err)
	}

	comments := []Comment{{
		ID:        "comment1",
		Author:    "Author1",
		Score:     -100,
		Permalink: "https://example.org/comment1",
		Sub:       "sub",
		Created:   time.Now().Round(time.Second),
		Body:      "This is the first test comment.",
	}, {
		ID:        "comment2",
		Author:    "Author2",
		Score:     -70,
		Permalink: "https://example.org/comment2",
		Sub:       "sub",
		Created:   time.Now().Round(time.Second),
		Body:      "This is the second test comment.",
	}, {
		ID:        "comment3",
		Author:    "Author1",
		Score:     75,
		Permalink: "https://example.org/comment3",
		Sub:       "sub",
		Created:   time.Now().Round(time.Second),
		Body:      "This is the third test comment.",
	}, {
		ID:        "comment4",
		Author:    "Author2",
		Score:     -140,
		Permalink: "https://example.org/comment4",
		Sub:       "sub",
		Created:   time.Now().Round(time.Second).Add(-2 * time.Hour),
		Body:      "This is the fourth test comment.",
	}, {
		ID:        "comment5",
		Author:    "Author3",
		Score:     -340,
		Permalink: "https://example.org/comment5",
		Sub:       "sub",
		Created:   time.Now().Round(time.Second).Add(-2 * time.Hour),
		Body:      "This is the fifth test comment.",
	}}

	start := time.Now().Round(time.Second).Add(-time.Hour)
	end := time.Now().Round(time.Second).Add(time.Hour)
	max_age := 24 * time.Hour

	// Save comments
	t.Run("save", func(t *testing.T) {
		if _, err := s.SaveCommentsUpdateUser(ctx, []Comment{comments[0], comments[2]}, author1, max_age); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SaveCommentsUpdateUser(ctx, []Comment{comments[1], comments[3]}, author2, max_age); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SaveCommentsUpdateUser(ctx, []Comment{comments[4]}, author3, max_age); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("read", func(t *testing.T) {
		comments, err := s.GetCommentsBelowBetween(ctx, -50, start, end)
		if err != nil {
			t.Fatal(err)
		}

		if !(len(comments) == 2 && comments[0].ID == "comment1" && comments[1].ID == "comment2" &&
			comments[0].Author == author1.Name && comments[1].Author == author2.Name &&
			comments[0].Score < 0 && comments[1].Score < 0) {
			t.Errorf("should have got two comments, one from author1, one from comment2, with negative scores, not %v", comments)
		}
	})

	t.Run("statistics only include negative comments", func(t *testing.T) {
		stats, err := s.StatsBetween(ctx, 0, start, end)
		if err != nil {
			t.Fatal(err)
		}
		if !(len(stats) == 2 && stats[author1.Name].Count == 1 && stats[author1.Name].Average == -100 &&
			stats[author2.Name].Average == -70 && stats[author2.Name].Count == 1) {
			t.Errorf("expected two users with average -70 and -100, not %v", stats)
		}
	})

	t.Run("update and read inactivity", func(t *testing.T) {
		if err := s.UpdateInactiveStatus(ctx, time.Hour); err != nil {
			t.Fatal(err)
		}

		active, err := s.ListActiveUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

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

	t.Run("purge", func(t *testing.T) {
		err := s.PurgeUser(ctx, "Author1")
		if err != nil {
			t.Fatal(err)
		}

		active, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if len(active) != 2 {
			t.Errorf("after purge of Author1 there should only be Author2 and Author3 left, not %v", active)
		}
	})

	t.Run("purge fail", func(t *testing.T) {
		err := s.PurgeUser(ctx, "NotUser")
		if err == nil {
			t.Error("NotUser doesn't exist and should lead to an error")
		}

		active, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if len(active) != 2 {
			t.Errorf("list of users should be left unchanged, instead of getting %v", active)
		}

		comments, err := s.GetCommentsBelowBetween(ctx, -50, start, end)
		if err != nil {
			t.Fatal(err)
		}

		if len(comments) != 1 {
			t.Errorf("comments should be left unchanged, instead of getting %v", comments)
		}
	})

	t.Run("get karma", func(t *testing.T) {
		if _, err := s.GetPositiveKarma(ctx, "Author2"); err != nil {
			if err != ErrNoComment {
				t.Fatal(err)
			}
		}

		if negative, err := s.GetNegativeKarma(ctx, "Author2"); err != nil {
			t.Fatal(err)
		} else if expected := int64(-70 + -140); negative != expected {
			t.Errorf("Author1's negative karma should be %d, not %d", expected, negative)
		}
	})

}
