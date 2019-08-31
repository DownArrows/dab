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
func (cf CompendiumFactory) Compendium(conn *SQLiteConn) (*Compendium, error) {
	c := &Compendium{
		NbTop:    cf.NbTop,
		Timezone: cf.Timezone,
		Version:  Version,
	}

	err := conn.WithTx(func() error {
		var err error
		var stats StatsCollection

		c.Users, err = cf.storage.ListRegisteredUsers(conn)
		if err != nil {
			return err
		}

		stats, err = cf.storage.CompendiumPerUser(conn)
		if err != nil {
			return err
		}
		c.All = stats.ToView(c.Timezone)

		stats, err = cf.storage.CompendiumPerUserNegative(conn)
		if err != nil {
			return err
		}
		c.Negative = stats.ToView(c.Timezone)

		c.rawTopComments, err = cf.storage.TopComments(conn, c.NbTop)
		return err
	})
	if err != nil {
		return nil, err
	}

	nbUsers := len(c.Users)
	for i := 0; i < nbUsers; i++ {
		c.Users[i] = c.Users[i].InTimezone(cf.Timezone)
	}

	return c, nil
}

// User returs a data structure that describes the compendium page for a single user.
func (cf CompendiumFactory) User(conn *SQLiteConn, user User) (*CompendiumUser, error) {
	cu := &CompendiumUser{
		Compendium: Compendium{
			NbTop:    cf.NbTop,
			Timezone: cf.Timezone,
			Users:    []User{user},
			Version:  Version,
		},
	}

	err := conn.WithTx(func() error {
		var err error
		var statsColl StatsCollection

		statsColl, err = cf.storage.CompendiumUserPerSub(conn, user.Name)
		if err != nil {
			return err
		}
		cu.All = statsColl.ToView(cu.Timezone)
		cu.Summary = statsColl.Stats().ToView(0, cu.Timezone)

		statsColl, err = cf.storage.CompendiumUserPerSubNegative(conn, user.Name)
		if err != nil {
			return err
		}
		cu.Negative = statsColl.ToView(cu.Timezone)
		cu.SummaryNegative = statsColl.Stats().ToView(0, cu.Timezone)

		cu.rawTopComments, err = cf.storage.TopCommentsUser(conn, user.Name, cu.NbTop)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return cu, nil
}

// Compendium describes the basic data of a page of the compendium.
// Specific pages may use it directly or extend it.
type Compendium struct {
	All                  []StatsView
	CommentBodyConverter CommentBodyConverter
	NbTop                uint
	Negative             []StatsView
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

// CompendiumUser describes the compendium page for a single user.
type CompendiumUser struct {
	Compendium
	Summary         StatsView
	SummaryNegative StatsView
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
