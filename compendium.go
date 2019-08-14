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

func (c CompendiumFactory) User(ctx context.Context, user User) (*CompendiumUser, error) {
	stats := &CompendiumUser{
		NbTop:           c.NbTop,
		Summary:         &CompendiumDetails{},
		SummaryNegative: &CompendiumDetails{},
		Timezone:        c.Timezone,
		User:            user,
		Version:         Version,
	}

	err := c.storage.WithTx(ctx, func(conn *SQLiteConn) error {
		if details, err := c.storage.CompendiumUserPerSub(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.All = details
		}

		if details, err := c.storage.CompendiumUserPerSubNegative(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.Negative = details
		}

		if comments, err := c.storage.UserTopComments(conn, stats.User.Name, stats.NbTop); err != nil {
			return err
		} else {
			stats.RawTopComments = comments
		}

		if details, err := c.storage.CompendiumUserSummary(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.Summary = details
		}

		if details, err := c.storage.CompendiumUserSummaryNegative(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.SummaryNegative = details
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	c.normalizeDetails(stats.Summary, 0)
	c.normalizeDetails(stats.SummaryNegative, 0)

	for i, details := range stats.All {
		c.normalizeDetails(&(details.CompendiumDetails), i+1)
	}

	for i, details := range stats.Negative {
		c.normalizeDetails(&(details.CompendiumDetails), i+1)
	}

	stats.User = stats.User.InTimezone(c.Timezone)

	return stats, nil
}

func (c CompendiumFactory) normalizeDetails(details *CompendiumDetails, n int) {
	details.Average = math.Round(details.Average)
	details.Latest = details.Latest.In(c.Timezone)
	details.Number = uint(n)
}
