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

	dir, err := ioutil.TempDir("", "dab-test-crud-users")
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

	users := []User{{
		Name:    "User1",
		Created: time.Now().Round(time.Second).Add(-6 * time.Hour),
	}, {
		Name:    "User2",
		Created: time.Now().Round(time.Second).Add(-5 * time.Hour),
	}}

	t.Run("add", func(t *testing.T) {
		for _, user := range users {
			if err := s.AddUser(ctx, user.Name, false, user.Created); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("get", func(t *testing.T) {
		query := s.GetUser(ctx, users[0].Name)
		if query.Error != nil {
			t.Fatal(query.Error)
		}

		user := query.User
		if user.Name != users[0].Name {
			t.Errorf("user should be named %q instead of %q", users[0].Name, user.Name)
		}
		if user.Created != users[0].Created {
			t.Errorf("user should have been created at %v instead of %v", users[0].Created, user.Created)
		}
	})

	t.Run("list", func(t *testing.T) {
		list, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if len(list) != len(users) {
			t.Errorf("listing should contain %d users, not %d: %+v", len(users), len(list), list)
		}
	})

	// Leave this case at the end so as not to complicate the previous ones.
	t.Run("delete", func(t *testing.T) {
		err := s.DelUser(ctx, users[1].Name)
		if err != nil {
			t.Fatal(err)
		}

		list, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if len(list) != len(users)-1 {
			t.Errorf("there should be only %d user(s) left, not %d: %+v", len(users)-1, len(list), list)
		}
	})
}

func TestCRUDComments(t *testing.T) {
	t.Parallel()

	dir, err := ioutil.TempDir("", "dab-test-crud-comments")
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

	// Test data
	report_start := time.Now().Round(time.Second).Add(-time.Hour)
	report_end := time.Now().Round(time.Second).Add(time.Hour)
	max_age := 24 * time.Hour
	report_cutoff := int64(-50)
	nb_reported := 3

	users := []User{{
		Name:    "User1",
		Created: time.Now().Round(time.Second).Add(-6 * time.Hour),
	}, {
		Name:    "User2",
		Created: time.Now().Round(time.Second).Add(-5 * time.Hour),
	}, {
		Name:    "User3",
		Created: time.Now().Round(time.Second).Add(-4 * time.Hour),
	}, {
		Name:    "InactiveUser",
		Created: time.Now().Round(time.Second).Add(-7 * time.Hour),
	}}

	users_map := make(map[string]User)
	for _, user := range users {
		users_map[user.Name] = user
	}

	data := map[User][]Comment{
		users[0]: {{
			ID:        "comment1",
			Author:    users[0].Name,
			Score:     -100,
			Permalink: "https://example.org/comment1",
			Sub:       "A",
			Created:   time.Now().Round(time.Second),
			Body:      "This is the first test comment.",
		}, {
			ID:        "comment3",
			Author:    users[0].Name,
			Score:     75,
			Permalink: "https://example.org/comment3",
			Sub:       "B",
			Created:   time.Now().Round(time.Second),
			Body:      "This is the third test comment.",
		}},
		users[1]: {{
			ID:        "comment2",
			Author:    users[1].Name,
			Score:     -70,
			Permalink: "https://example.org/comment2",
			Sub:       "A",
			Created:   time.Now().Round(time.Second),
			Body:      "This is the second test comment.",
		}, {
			ID:        "comment4",
			Author:    users[1].Name,
			Score:     -140,
			Permalink: "https://example.org/comment4",
			Sub:       "B",
			Created:   time.Now().Round(time.Second).Add(-2 * time.Hour),
			Body:      "This is the fourth test comment.",
		}, {
			ID:        "comment6",
			Author:    users[1].Name,
			Score:     15,
			Permalink: "https://example.org/comment6",
			Sub:       "A",
			Created:   time.Now().Round(time.Second),
			Body:      "This is the sixth test comment.",
		}},
		users[2]: {{
			ID:        "comment5",
			Author:    users[2].Name,
			Score:     -340,
			Permalink: "https://example.org/comment5",
			Sub:       "B",
			Created:   time.Now().Round(time.Second).Add(-2 * time.Hour),
			Body:      "This is the fifth test comment.",
		}, {
			ID:        "comment7",
			Author:    users[2].Name,
			Score:     report_cutoff,
			Permalink: "https://example.org/comment7",
			Sub:       "A",
			Created:   time.Now().Round(time.Second),
			Body:      "This is the seventh test comment.",
		}, {
			ID:        "comment8",
			Author:    users[2].Name,
			Score:     report_cutoff + 1,
			Permalink: "https://example.org/comment8",
			Sub:       "B",
			Created:   time.Now().Round(time.Second),
			Body:      "This is the eighth test comment.",
		}},
		users[3]: {{
			ID:        "comment9",
			Author:    users[3].Name,
			Score:     -89,
			Permalink: "https://example.org/comment9",
			Sub:       "A",
			Created:   time.Now().Round(time.Second).Add(-10 * time.Hour),
			Body:      "This is the ninth test comment.",
		}},
	}

	t.Run("save users", func(t *testing.T) {
		t.Helper()
		for _, user := range users {
			if err := s.AddUser(ctx, user.Name, false, user.Created); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("save", func(t *testing.T) {
		for user, comments := range data {
			if _, err := s.SaveCommentsUpdateUser(ctx, comments, user, max_age); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("read", func(t *testing.T) {
		err := s.WithConn(ctx, func(conn *SQLiteConn) error {
			comments, err := s.GetCommentsBelowBetween(conn, report_cutoff, report_start, report_end)
			if err != nil {
				return err
			}

			scores_ok := true
			for _, comment := range comments {
				if comment.Score > report_cutoff {
					scores_ok = false
				}
			}
			if !scores_ok {
				t.Fatalf("no comment should have a score above %d: %+v", report_cutoff, comments)
			}

			if len(comments) != nb_reported {
				t.Fatalf("should have gotten %d comments, instead got %d: %+v", nb_reported, len(comments), comments)
			}

			var expected_sum int64
			for _, comments := range data {
				for _, comment := range comments {
					if comment.Score <= report_cutoff && comment.Created.After(report_start) {
						expected_sum += comment.Score
					}
				}
			}
			var actual_sum int64
			for _, comment := range comments {
				actual_sum += comment.Score
			}
			if actual_sum != expected_sum {
				t.Fatalf("the sum of the scores of the reported comments should be %d, not %d: %+v", expected_sum, actual_sum, comments)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("statistics", func(t *testing.T) {
		err := s.WithConn(ctx, func(conn *SQLiteConn) error {
			all_stats, err := s.StatsBetween(conn, report_cutoff, report_start, report_end)
			if err != nil {
				return err
			}

			expected_nb_stats := 3
			if len(all_stats) != expected_nb_stats {
				t.Errorf("expected statistics for %d users, not %d: %+v", expected_nb_stats, len(all_stats), all_stats)
			}

			for _, stats := range all_stats {
				user := users_map[stats.Name]
				comments := data[user]
				// Computing averages would be a bit too involved for a test case;
				// let's assume that if averages are broken then the rest is likely to also be broken.
				var count uint64
				var delta int64
				for _, comment := range comments {
					if comment.Score < 0 && comment.Created.After(report_start) {
						count++
						delta += comment.Score
					}
				}
				if count != stats.Count {
					t.Errorf("expected count for user %s to be %d, not %d", user.Name, count, stats.Count)
				}
				if delta != stats.Delta {
					t.Errorf("expected delta for user %s to be %d, not %d", user.Name, delta, stats.Delta)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
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

		expected_active := 3
		if !(len(active) == expected_active) {
			t.Errorf("expected %d users to be active, not %d: %v", expected_active, len(active), active)
		}
	})

	// See end of the list of test cases for a successful purge.
	t.Run("purge fail", func(t *testing.T) {
		err := s.PurgeUser(ctx, "NotUser")
		if err == nil {
			t.Error("NotUser doesn't exist and should lead to an error")
		}

		active, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if len(active) != len(users) {
			t.Errorf("list of users should be left unchanged, instead of getting %v", active)
		}

		err = s.WithConn(ctx, func(conn *SQLiteConn) error {
			comments, err := s.GetCommentsBelowBetween(conn, report_cutoff, report_start, report_end)
			if err != nil {
				return err
			}
			if len(comments) != nb_reported {
				t.Errorf("number of reported comments should be left unchanged, instead %d", len(comments))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("get karma", func(t *testing.T) {
		total, negative, err := s.GetKarma(ctx, users[1].Name)
		if err != nil {
			t.Fatal(err)
		}

		var expected_total int64
		var expected_negative int64
		for _, comment := range data[users[1]] {
			expected_total += comment.Score
			if comment.Score < 0 {
				expected_negative += comment.Score
			}
		}

		if expected_total != total || expected_negative != negative {
			t.Errorf("karma for %s should be %d/%d, not %d/%d", users[1].Name, expected_negative, expected_total, negative, total)
		}
	})

	// Leave that test case at the end so as not to complicate the previous ones.
	t.Run("purge", func(t *testing.T) {
		err := s.PurgeUser(ctx, users[0].Name)
		if err != nil {
			t.Fatal(err)
		}

		active, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if len(active) != len(users)-1 {
			t.Errorf("after purge there should be %d users left, not %d: %+v", len(users)-1, len(active), active)
		}

		conn, err := s.db.GetConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		comments, err := s.GetCommentsBelowBetween(conn, report_cutoff, report_start, report_end)
		if err != nil {
			t.Fatal(err)
		}

		if len(comments) == nb_reported {
			t.Error("number of reported comments should not have changed")
		}
	})
}
