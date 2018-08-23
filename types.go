package main

import (
	"math"
	"sort"
	"strings"
	"time"
)

type Comment struct {
	Id        string
	Author    string
	Score     int64
	Permalink string
	Sub       string `json:"subreddit"`
	// This is only used for decoding JSON, otherwise user Created
	RawCreated float64   `json:"created_utc"`
	Created    time.Time `json:"-"` // This field exists in reddit's JSON with another type and meaning
	Body       string
}

func (comment Comment) FinishDecoding() Comment {
	comment.Created = time.Unix(int64(comment.RawCreated), 0)
	comment.RawCreated = 0
	return comment
}

type User struct {
	Name      string
	Hidden    bool
	New       bool
	Suspended bool
	Created   time.Time
	Added     time.Time
	Position  string
	Inactive  bool
}

func (user *User) Username(username string) bool {
	return strings.ToLower(user.Name) == strings.ToLower(username)
}

type UserQuery struct {
	User   User
	Exists bool
	Error  error
}

type RedditAuth struct {
	Username string
	Password string
	Id       string
	Key      string
}

type UserStats struct {
	Author  string
	Average float64
	Delta   int64
	Count   uint64
}

type Stats map[string]UserStats

func (s Stats) DeltasToScores() Scores {
	result := make([]GenStats, 0, len(s))
	for name, data := range s {
		result = append(result, GenStats{
			Author: name,
			Score:  data.Delta,
			Count:  data.Count,
		})
	}
	return Scores{v: result}
}

func (s Stats) AveragesToScores() Scores {
	result := make([]GenStats, 0, len(s))
	for name, data := range s {
		result = append(result, GenStats{
			Author: name,
			Score:  int64(math.Round(data.Average)),
			Count:  data.Count,
		})
	}
	return Scores{v: result}
}

type GenStats struct {
	Author string
	Count  uint64
	Score  int64
}

// We have to define this to be able to use sort.Sort
type Scores struct {
	v []GenStats
}

func (s Scores) Len() int {
	return len(s.v)
}

func (s Scores) Less(i, j int) bool {
	return s.v[i].Score < s.v[j].Score
}

func (s Scores) Swap(i, j int) {
	s.v[i], s.v[j] = s.v[j], s.v[i]
}

func (s Scores) Sort() []GenStats {
	sort.Sort(s)
	return s.v
}
