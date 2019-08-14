package main

import (
	"context"
	"math"
	"time"
)

type Compendium struct {
	NbTop    uint
	storage  CompendiumStorage
	Timezone *time.Location
}

func NewCompendium(storage CompendiumStorage, conf CompendiumConf) Compendium {
	return Compendium{
		NbTop:    conf.NbTop,
		storage:  storage,
		Timezone: conf.Timezone.Value,
	}
}

func (c Compendium) UserStats(ctx context.Context, user User) (*CompendiumUserStats, error) {
	stats := &CompendiumUserStats{
		NbTop:           c.NbTop,
		Summary:         &CompendiumUserStatsDetails{},
		SummaryNegative: &CompendiumUserStatsDetails{},
		Timezone:        c.Timezone,
		User:            user,
		Version:         Version,
	}

	err := c.storage.WithTx(ctx, func(conn *SQLiteConn) error {
		if details, err := c.storage.CompendiumUserStatsPerSub(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.All = details
		}

		if details, err := c.storage.CompendiumUserStatsPerSubNegative(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.Negative = details
		}

		if comments, err := c.storage.UserTopComments(conn, stats.User.Name, stats.NbTop); err != nil {
			return err
		} else {
			stats.RawTopComments = comments
		}

		if details, err := c.storage.CompendiumUserStatsSummary(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.Summary = details
		}

		if details, err := c.storage.CompendiumUserStatsSummaryNegative(conn, stats.User.Name); err != nil {
			return err
		} else {
			stats.SummaryNegative = details
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	c.normalizeUserStatsDetails(stats.Summary, 0)
	c.normalizeUserStatsDetails(stats.SummaryNegative, 0)

	for i, details := range stats.All {
		c.normalizeUserStatsDetails(&(details.CompendiumUserStatsDetails), i+1)
	}

	for i, details := range stats.Negative {
		c.normalizeUserStatsDetails(&(details.CompendiumUserStatsDetails), i+1)
	}

	stats.User = stats.User.InTimezone(c.Timezone)

	return stats, nil
}

func (c Compendium) normalizeUserStatsDetails(details *CompendiumUserStatsDetails, n int) {
	details.Average = math.Round(details.Average)
	details.Latest = details.Latest.In(c.Timezone)
	details.Number = uint(n)
}
