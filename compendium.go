package main

import (
	"math"
	"time"
)

// CompendiumFactory generates data structures for any page of the compendium.
type CompendiumFactory struct {
	NbTop    uint           // Number of most downvoted comments
	Timezone *time.Location // Timezone of the dates
}

// NewCompendiumFactory returns a new CompendiumFactory.
func NewCompendiumFactory(conf CompendiumConf) CompendiumFactory {
	return CompendiumFactory{
		NbTop:    conf.NbTop,
		Timezone: conf.Timezone.Value,
	}
}

// Index returns the data structure that describes the compendium's index.
func (cf CompendiumFactory) Index(conn StorageConn) (Compendium, error) {
	ci := Compendium{
		NbTop:    cf.NbTop,
		Timezone: cf.Timezone,
		Version:  Version,
	}

	err := conn.WithTx(func() error {
		var err error

		ci.Users, err = conn.ListRegisteredUsers()
		if err != nil {
			return err
		}

		all, negative, err := conn.CompendiumPerUser()
		if err != nil {
			return err
		}
		ci.All = all.ToView(ci.Timezone)
		ci.Negative = negative.OrderBy(func(a, b Stats) bool { return a.Sum < b.Sum }).ToView(ci.Timezone)

		ci.rawComments, err = conn.Comments(Pagination{Limit: ci.NbTop})
		return err
	})
	if err != nil {
		return ci, err
	}

	nbUsers := len(ci.Users)
	for i := 0; i < nbUsers; i++ {
		ci.Users[i] = ci.Users[i].InTimezone(cf.Timezone)
	}

	return ci, nil
}

// User returns a data structure that describes the compendium page for a single user.
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

		cu.Negative = negative.OrderBy(func(a, b Stats) bool { return a.Sum < b.Sum }).ToView(cu.Timezone)
		cu.SummaryNegative = negative.Stats().ToView(0, cu.Timezone)

		cu.rawComments, err = conn.UserComments(user.Name, Pagination{Limit: cu.NbTop})
		return err
	})
	return cu, err
}

// UserComments returns a page of comments for a user.
func (cf CompendiumFactory) UserComments(conn StorageConn, user User, page Pagination) (CompendiumUser, error) {
	comments, err := conn.UserComments(user.Name, page)
	cu := CompendiumUser{
		Compendium: Compendium{
			NbTop:       page.Limit,
			Offset:      page.Offset,
			Timezone:    cf.Timezone,
			Users:       []User{user},
			Version:     Version,
			rawComments: comments,
		},
	}
	return cu, err
}

// Compendium describes the basic data of a page of the compendium.
// Specific pages may use it directly or extend it.
type Compendium struct {
	All         []StatsView    // Statistics about every comments
	NbTop       uint           // Number of most downvoted comments
	Negative    []StatsView    // Statistics about comments with a negative score
	Offset      uint           // Offset in the rank of the comments
	Timezone    *time.Location // Timezone of the dates
	Users       []User         // Users in the compendium
	Version     SemVer         // Version of the application
	rawComments []Comment

	CommentBodyConverter CommentBodyConverter
}

// CommentsLen returns the number of top comments without generating them.
func (c Compendium) CommentsLen() int {
	return len(c.rawComments)
}

// Comments generates the views for the top comments.
func (c Compendium) Comments() []CommentView {
	offset := uint64(c.Offset)
	views := make([]CommentView, 0, len(c.rawComments))
	for i, comment := range c.rawComments {
		view := comment.ToView(uint64(i+1)+offset, c.Timezone, c.CommentBodyConverter)
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

// NextOffset returns the offset for the next page.
func (c Compendium) NextOffset() uint {
	return c.NbTop + c.Offset
}

// CompendiumUser describes the compendium page for a single user.
type CompendiumUser struct {
	Compendium
	Summary         StatsView // Statistics summarizing the user's activity
	SummaryNegative StatsView // Statistics summarizing the user's activity based only on comments with a negative score
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
