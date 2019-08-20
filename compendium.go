package main

import (
	"math"
	"time"
)

// CompendiumFactory generates data structures for any page of the compendium.
type CompendiumFactory struct {
	// Number of most downvoted comments.
	NbTop   uint
	storage CompendiumStorage
	// Timezone of the dates.
	Timezone *time.Location
}

// NewCompendiumFactory returns a new CompendiumFactory.
func NewCompendiumFactory(storage CompendiumStorage, conf CompendiumConf) CompendiumFactory {
	return CompendiumFactory{
		NbTop:    conf.NbTop,
		storage:  storage,
		Timezone: conf.Timezone.Value,
	}
}

// Compendium returns the data structure that describes the compendium's index.
func (c CompendiumFactory) Compendium(conn *SQLiteConn) (*Compendium, error) {
	stats := &Compendium{
		NbTop:    c.NbTop,
		Timezone: c.Timezone,
		Version:  Version,
	}

	err := conn.WithTx(func() error {
		var err error
		stats.Users, err = c.storage.ListRegisteredUsers(conn)
		if err != nil {
			return err
		}

		stats.All, err = c.storage.CompendiumPerUser(conn)
		if err != nil {
			return err
		}

		stats.Negative, err = c.storage.CompendiumPerUserNegative(conn)
		if err != nil {
			return err
		}

		stats.rawTopComments, err = c.storage.TopComments(conn, stats.NbTop)
		return err
	})
	if err != nil {
		return nil, err
	}

	stats.normalize()

	return stats, nil
}

// User returs a data structure that describes the compendium page for a single user.
func (c CompendiumFactory) User(conn *SQLiteConn, user User) (*CompendiumUser, error) {
	stats := &CompendiumUser{
		Compendium: Compendium{
			NbTop:    c.NbTop,
			Timezone: c.Timezone,
			Users:    []User{user},
			Version:  Version,
		},
		Summary:         &CompendiumDetails{},
		SummaryNegative: &CompendiumDetails{},
	}

	err := conn.WithTx(func() error {
		var err error
		stats.All, err = c.storage.CompendiumUserPerSub(conn, user.Name)
		if err != nil {
			return err
		}

		stats.Negative, err = c.storage.CompendiumUserPerSubNegative(conn, user.Name)
		if err != nil {
			return err
		}

		stats.rawTopComments, err = c.storage.TopCommentsUser(conn, user.Name, stats.NbTop)
		if err != nil {
			return err
		}

		stats.Summary, err = c.storage.CompendiumUserSummary(conn, user.Name)
		if err != nil {
			return err
		}

		stats.SummaryNegative, err = c.storage.CompendiumUserSummaryNegative(conn, user.Name)
		return err
	})
	if err != nil {
		return nil, err
	}

	stats.normalize()

	return stats, nil
}

// Compendium describes the basic data of a page of the compendium.
// Specific pages may use it directly or extend it.
type Compendium struct {
	All                  []*CompendiumDetailsTagged
	CommentBodyConverter CommentBodyConverter
	NbTop                uint
	Negative             []*CompendiumDetailsTagged
	rawTopComments       []Comment
	Timezone             *time.Location
	Users                []User
	Version              SemVer
}

// TopCommentsLen returns the number of top comments without generating them.
func (c *Compendium) TopCommentsLen() int {
	return len(c.rawTopComments)
}

// TopComments generates the views for the top comments.
func (c *Compendium) TopComments() []CommentView {
	views := make([]CommentView, 0, len(c.rawTopComments))
	for i, comment := range c.rawTopComments {
		view := comment.ToView(uint(i+1), c.Timezone, c.CommentBodyConverter)
		views = append(views, view)
	}
	return views
}

// HiddenUsersLen returns the number of hidden users.
func (c *Compendium) HiddenUsersLen() int {
	var nb int
	for _, user := range c.Users {
		if user.Hidden {
			nb++
		}
	}
	return nb
}

func (c *Compendium) normalize() {
	for i, details := range c.All {
		details.Normalize(uint(i+1), c.Timezone)
	}

	for i, details := range c.Negative {
		details.Normalize(uint(i+1), c.Timezone)
	}

	nbUsers := len(c.Users)
	for i := 0; i < nbUsers; i++ {
		c.Users[i] = c.Users[i].InTimezone(c.Timezone)
	}
}

// CompendiumUser describes the compendium page for a single user.
type CompendiumUser struct {
	Compendium
	Summary         *CompendiumDetails
	SummaryNegative *CompendiumDetails
}

// User returns the single User being described.
func (cu *CompendiumUser) User() User {
	return cu.Users[0]
}

// PercentageNegative returns the rounded percentage of comments in the negatives.
func (cu *CompendiumUser) PercentageNegative() int64 {
	if cu.Summary.Count == 0 || cu.SummaryNegative.Count == 0 {
		return 0
	}
	return int64(math.Round(100 * float64(cu.SummaryNegative.Count) / float64(cu.Summary.Count)))
}

func (cu *CompendiumUser) normalize() {
	cu.Compendium.normalize()
	cu.Summary.Normalize(0, cu.Timezone)
	cu.SummaryNegative.Normalize(0, cu.Timezone)
}
