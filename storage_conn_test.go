package main

import (
	"context"
	"testing"
	"time"
)

func TestCRUDUsers(t *testing.T) {
	t.Parallel()

	path := ":memory:"
	ctx := context.Background()

	_, conn, err := NewStorage(ctx, NewTestLevelLogger(t), StorageConf{Path: path, SecretsPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	users := []User{{
		Name:    "User1",
		Created: time.Now().Round(time.Second).Add(-6 * time.Hour),
	}, {
		Name:    "User2",
		Created: time.Now().Round(time.Second).Add(-5 * time.Hour),
	}}

	t.Run("add", func(t *testing.T) {
		for _, user := range users {
			if query := conn.AddUser(UserQuery{User: user}); query.Error != nil {
				t.Fatal(query.Error)
			}
		}
	})

	t.Run("get", func(t *testing.T) {
		query := conn.GetUser(users[0].Name)
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
		if user.Notes != users[0].Notes {
			t.Errorf("user should have attached notes %q instead of %q", users[0].Notes, user.Notes)
		}
	})

	t.Run("list", func(t *testing.T) {
		list, err := conn.ListUsers()
		if err != nil {
			t.Fatal(err)
		}

		if len(list) != len(users) {
			t.Errorf("listing should contain %d users, not %d: %+v", len(users), len(list), list)
		}
	})

	// Leave this case at the end so as not to complicate the previous ones.
	t.Run("delete", func(t *testing.T) {
		err := conn.DelUser(users[1].Name)
		if err != nil {
			t.Fatal(err)
		}

		list, err := conn.ListUsers()
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

	path := ":memory:"
	ctx := context.Background()

	_, conn, err := NewStorage(ctx, NewTestLevelLogger(t), StorageConf{Path: path, SecretsPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Test data
	reportStart := time.Now().Round(time.Second).Add(-time.Hour)
	reportEnd := time.Now().Round(time.Second).Add(time.Hour)
	maxAge := 24 * time.Hour
	reportCutoff := int64(-50)
	nbReported := 3

	users := []User{{
		Name:    "User1",
		Created: time.Now().Round(time.Second).Add(-6 * time.Hour),
		Notes:   "test1",
	}, {
		Name:    "User2",
		Created: time.Now().Round(time.Second).Add(-5 * time.Hour),
		Notes:   "test2",
	}, {
		Name:    "User3",
		Created: time.Now().Round(time.Second).Add(-4 * time.Hour),
		Notes:   "test3",
	}, {
		Name:    "InactiveUser",
		Created: time.Now().Round(time.Second).Add(-7 * time.Hour),
		Notes:   "test4",
	}}

	usersMap := make(map[string]User)
	for _, user := range users {
		usersMap[user.Name] = user
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
			Score:     reportCutoff,
			Permalink: "https://example.org/comment7",
			Sub:       "A",
			Created:   time.Now().Round(time.Second),
			Body:      "This is the seventh test comment.",
		}, {
			ID:        "comment8",
			Author:    users[2].Name,
			Score:     reportCutoff + 1,
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
			if query := conn.AddUser(UserQuery{User: user}); query.Error != nil {
				t.Fatal(query.Error)
			}
		}
	})

	t.Run("save", func(t *testing.T) {
		for user, comments := range data {
			if _, err := conn.SaveCommentsUpdateUser(comments, user, maxAge); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("read", func(t *testing.T) {
		comments, err := conn.GetCommentsBelowBetween(reportCutoff, reportStart, reportEnd)
		if err != nil {
			t.Fatal(err)
		}

		scoresOk := true
		for _, comment := range comments {
			if comment.Score > reportCutoff {
				scoresOk = false
			}
		}
		if !scoresOk {
			t.Fatalf("no comment should have a score above %d: %+v", reportCutoff, comments)
		}

		if len(comments) != nbReported {
			t.Fatalf("should have gotten %d comments, instead got %d: %+v", nbReported, len(comments), comments)
		}

		var expectedSum int64
		for _, comments := range data {
			for _, comment := range comments {
				if comment.Score <= reportCutoff && comment.Created.After(reportStart) {
					expectedSum += comment.Score
				}
			}
		}
		var actualSum int64
		for _, comment := range comments {
			actualSum += comment.Score
		}
		if actualSum != expectedSum {
			t.Fatalf("the sum of the scores of the reported comments should be %d, not %d: %+v", expectedSum, actualSum, comments)
		}
	})

	t.Run("statistics", func(t *testing.T) {
		allStats, err := conn.StatsBetween(reportStart, reportEnd)
		if err != nil {
			t.Fatal(err)
		}

		expectedNbStats := 3
		if len(allStats) != expectedNbStats {
			t.Errorf("expected statistics for %d users, not %d: %+v", expectedNbStats, len(allStats), allStats)
		}

		for _, stats := range allStats {
			user := usersMap[stats.Name]
			comments := data[user]
			// Computing averages would be a bit too involved for a test case;
			// let's assume that if averages are broken then the rest is likely to also be broken.
			var count uint64
			var delta int64
			for _, comment := range comments {
				if comment.Score < 0 && comment.Created.After(reportStart) {
					count++
					delta += comment.Score
				}
			}
			if count != stats.Count {
				t.Errorf("expected count for user %s to be %d, not %d", user.Name, count, stats.Count)
			}
			if delta != stats.Sum {
				t.Errorf("expected delta for user %s to be %d, not %d", user.Name, delta, stats.Sum)
			}
		}
	})

	t.Run("update and read inactivity", func(t *testing.T) {
		if err := conn.UpdateInactiveStatus(time.Hour); err != nil {
			t.Fatal(err)
		}

		active, err := conn.ListActiveUsers()
		if err != nil {
			t.Fatal(err)
		}

		expectedActive := 3
		if !(len(active) == expectedActive) {
			t.Errorf("expected %d users to be active, not %d: %v", expectedActive, len(active), active)
		}
	})

	// See end of the list of test cases for a successful purge.
	t.Run("purge fail", func(t *testing.T) {
		err := conn.PurgeUser("NotUser")
		if err == nil {
			t.Error("NotUser doesn't exist and should lead to an error")
		}

		active, err := conn.ListUsers()
		if err != nil {
			t.Fatal(err)
		}

		if len(active) != len(users) {
			t.Errorf("list of users should be left unchanged, instead of getting %v", active)
		}

		comments, err := conn.GetCommentsBelowBetween(reportCutoff, reportStart, reportEnd)
		if err != nil {
			t.Fatal(err)
		}
		if len(comments) != nbReported {
			t.Errorf("number of reported comments should be left unchanged, instead %d", len(comments))
		}
	})

	t.Run("get karma", func(t *testing.T) {
		total, negative, err := conn.GetKarma(users[1].Name)
		if err != nil {
			t.Fatal(err)
		}

		var expectedTotal int64
		var expectedNegative int64
		for _, comment := range data[users[1]] {
			expectedTotal += comment.Score
			if comment.Score < 0 {
				expectedNegative += comment.Score
			}
		}

		if expectedTotal != total || expectedNegative != negative {
			t.Errorf("karma for %s should be %d/%d, not %d/%d", users[1].Name, expectedNegative, expectedTotal, negative, total)
		}
	})

	// Leave that test case at the end so as not to complicate the previous ones.
	t.Run("purge", func(t *testing.T) {
		err := conn.PurgeUser(users[0].Name)
		if err != nil {
			t.Fatal(err)
		}

		active, err := conn.ListUsers()
		if err != nil {
			t.Fatal(err)
		}

		if len(active) != len(users)-1 {
			t.Errorf("after purge there should be %d users left, not %d: %+v", len(users)-1, len(active), active)
		}

		comments, err := conn.GetCommentsBelowBetween(reportCutoff, reportStart, reportEnd)
		if err != nil {
			t.Fatal(err)
		}

		if len(comments) == nbReported {
			t.Error("number of reported comments should not have changed")
		}
	})
}
