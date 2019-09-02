package main

import (
	"math"
	"time"
)

// CompendiumFactory generates data structures for any page of the compendium.
type CompendiumFactory struct {
	// Number of most downvoted comments.
	NbTop uint
	// Timezone of the dates.
	Timezone *time.Location
}

// NewCompendiumFactory returns a new CompendiumFactory.
func NewCompendiumFactory(conf CompendiumConf) CompendiumFactory {
	return CompendiumFactory{
		NbTop:    conf.NbTop,
		Timezone: conf.Timezone.Value,
	}
}

// Compendium returns the data structure that describes the compendium's index.
func (cf CompendiumFactory) Compendium(conn StorageConn) (Compendium, error) {
	c := Compendium{
		NbTop:    cf.NbTop,
		Timezone: cf.Timezone,
		Version:  Version,
	}

	err := conn.WithTx(func() error {
		var err error

		c.Users, err = conn.ListRegisteredUsers()
		if err != nil {
			return err
		}

		all, negative, err := conn.CompendiumPerUser()
		if err != nil {
			return err
		}
		c.All = all.ToView(c.Timezone)
		c.Negative = negative.Filter(func(s Stats) bool { return s.Sum < 0 }).OrderBySum().ToView(c.Timezone)

		c.rawTopComments, err = conn.TopComments(c.NbTop)
		return err
	})
	if err != nil {
		return c, err
	}

	nbUsers := len(c.Users)
	for i := 0; i < nbUsers; i++ {
		c.Users[i] = c.Users[i].InTimezone(cf.Timezone)
	}

	return c, nil
}

// User returs a data structure that describes the compendium page for a single user.
func (cf CompendiumFactory) User(conn StorageConn, user User) (CompendiumUser, error) {
	cu := CompendiumUser{
		Compendium: Compendium{
			NbTop:    cf.NbTop,
			Timezone: cf.Timezone,
			Users:    []User{user},
			Version:  Version,
		},
	}

	err := conn.WithTx(func() error {
		all, rawNegative, err := conn.CompendiumUserPerSub(user.Name)
		if err != nil {
			return err
		}
		cu.All = all.ToView(cu.Timezone)
		cu.Summary = all.Stats().ToView(0, cu.Timezone)
		negative := rawNegative.Filter(func(s Stats) bool { return s.Sum < 0 })
		cu.Negative = negative.OrderBySum().ToView(cu.Timezone)
		cu.SummaryNegative = negative.Stats().ToView(0, cu.Timezone)

		cu.rawTopComments, err = conn.TopCommentsUser(user.Name, cu.NbTop)
		return err
	})
	return cu, err
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
func (c Compendium) TopCommentsLen() int {
	return len(c.rawTopComments)
}

// TopComments generates the views for the top comments.
func (c Compendium) TopComments() []CommentView {
	views := make([]CommentView, 0, len(c.rawTopComments))
	for i, comment := range c.rawTopComments {
		view := comment.ToView(uint(i+1), c.Timezone, c.CommentBodyConverter)
		views = append(views, view)
	}
	return views
}

// HiddenUsersLen returns the number of hidden users.
func (c Compendium) HiddenUsersLen() int {
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
func (cu CompendiumUser) User() User {
	return cu.Users[0]
}

// PercentageNegative returns the rounded percentage of comments in the negatives.
func (cu CompendiumUser) PercentageNegative() int64 {
	if cu.Summary.Count == 0 || cu.SummaryNegative.Count == 0 {
		return 0
	}
	return int64(math.Round(100 * float64(cu.SummaryNegative.Count) / float64(cu.Summary.Count)))
}
