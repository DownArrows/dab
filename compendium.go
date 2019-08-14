package main

import (
	"context"
	"math"
	"time"
)

type CompendiumFactory struct {
	NbTop    uint
	storage  CompendiumStorage
	Timezone *time.Location
}

func NewCompendiumFactory(storage CompendiumStorage, conf CompendiumConf) CompendiumFactory {
	return CompendiumFactory{
		NbTop:    conf.NbTop,
		storage:  storage,
		Timezone: conf.Timezone.Value,
	}
}

func (c CompendiumFactory) Compendium(ctx context.Context) (*Compendium, error) {
	stats := &Compendium{
		NbTop:    c.NbTop,
		Timezone: c.Timezone,
		Version:  Version,
	}

	err := c.storage.WithTx(ctx, func(conn *SQLiteConn) error {
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

	stats.Normalize()

	return stats, nil
}

func (c CompendiumFactory) User(ctx context.Context, user User) (*CompendiumUser, error) {
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

	err := c.storage.WithTx(ctx, func(conn *SQLiteConn) error {
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

	stats.Normalize()

	return stats, nil
}

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

func (c *Compendium) TopCommentsLen() int {
	return len(c.rawTopComments)
}

func (c *Compendium) TopComments() []CommentView {
	views := make([]CommentView, 0, len(c.rawTopComments))
	for i, comment := range c.rawTopComments {
		view := comment.ToView(uint(i+1), c.Timezone, c.CommentBodyConverter)
		views = append(views, view)
	}
	return views
}

func (c *Compendium) HiddenUsersLen() int {
	var nb int
	for _, user := range c.Users {
		if user.Hidden {
			nb++
		}
	}
	return nb
}

func (c *Compendium) Normalize() {
	for i, details := range c.All {
		details.Normalize(uint(i+1), c.Timezone)
	}

	for i, details := range c.Negative {
		details.Normalize(uint(i+1), c.Timezone)
	}

	nb_users := len(c.Users)
	for i := 0; i < nb_users; i++ {
		c.Users[i] = c.Users[i].InTimezone(c.Timezone)
	}
}

type CompendiumUser struct {
	Compendium
	Summary         *CompendiumDetails
	SummaryNegative *CompendiumDetails
}

func (cu *CompendiumUser) User() User {
	return cu.Users[0]
}

func (cu *CompendiumUser) PercentageNegative() int64 {
	if cu.Summary.Count == 0 || cu.SummaryNegative.Count == 0 {
		return 0
	}
	return int64(math.Round(100 * float64(cu.SummaryNegative.Count) / float64(cu.Summary.Count)))
}

func (cu *CompendiumUser) Normalize() {
	cu.Compendium.Normalize()
	cu.Summary.Normalize(0, cu.Timezone)
	cu.SummaryNegative.Normalize(0, cu.Timezone)
}
