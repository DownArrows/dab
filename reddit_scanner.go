package main

import (
	"context"
	"log"
	"time"
)

type RedditScanner struct {
	// dependencies
	api     RedditScannerAPI
	logger  *log.Logger
	storage RedditScannerStorage

	// communication with the outside
	suspensions chan User
	highScores  chan Comment

	// configuration
	fullScanInterval    time.Duration
	highScoreThreshold  int64
	inactivityThreshold time.Duration
	maxAge              time.Duration
	maxBatches          uint
	commentsLeeway      uint
}

func NewRedditScanner(
	logger *log.Logger,
	storage RedditScannerStorage,
	api RedditScannerAPI,
	conf RedditScannerConf,
) *RedditScanner {
	return &RedditScanner{
		api:     api,
		logger:  logger,
		storage: storage,

		commentsLeeway:      5,
		fullScanInterval:    conf.FullScanInterval.Value,
		highScoreThreshold:  conf.HighScoreThreshold,
		inactivityThreshold: conf.InactivityThreshold.Value,
		maxAge:              conf.MaxAge.Value,
		maxBatches:          conf.MaxBatches,
	}
}

func (rs *RedditScanner) Suspensions() <-chan User {
	if rs.suspensions == nil {
		rs.suspensions = make(chan User)
	}
	return rs.suspensions
}

func (rs *RedditScanner) HighScores() <-chan Comment {
	if rs.highScores == nil {
		rs.highScores = make(chan Comment)
	}
	return rs.highScores
}

func (rs *RedditScanner) Run(ctx context.Context) error {
	var last_full_scan time.Time

	rs.logger.Printf("starting comments scanner")

	for ctx.Err() == nil {

		now := time.Now().Round(0)
		full_scan := now.Sub(last_full_scan) >= rs.fullScanInterval

		users := rs.getUsersOrWait(ctx, full_scan)
		if len(users) == 0 {
			return ctx.Err()
		}

		if err := rs.Scan(ctx, users); err != nil {
			return err
		}

		if full_scan {
			last_full_scan = now
			if err := rs.storage.UpdateInactiveStatus(rs.inactivityThreshold); err != nil {
				return err
			}
		}

	}

	return ctx.Err()
}

func (rs *RedditScanner) Scan(ctx context.Context, users []User) error {
	for _, user := range users {

		for i := uint(0); i < rs.maxBatches; i++ {
			var err error
			var comments []Comment
			var limit uint
			last_scan := time.Now().Round(0).Sub(user.LastScanTime())

			if user.New || // if the user is new, we need to scan everything as fast as possible
				user.Position != "" || // we don't know how many relevant comments the next page has, so take as many as possible
				user.BatchSize+rs.commentsLeeway > MaxRedditListingLength || // don't request more than the maximum, else we'll look stupid
				last_scan > rs.maxAge { // use rs.maxAge as a heuristic to say if too much time has passed since the last scan
				limit = MaxRedditListingLength
			} else {
				limit = user.BatchSize + rs.commentsLeeway
			}

			comments, user, err = rs.api.UserComments(ctx, user, limit)
			if isCancellation(err) {
				return err
			} else if err != nil {
				rs.logger.Printf("error while scanning user %s: %v", user.Name, err)
			}

			user, err = rs.storage.SaveCommentsUpdateUser(comments, user, last_scan+rs.maxAge)
			if err != nil {
				rs.logger.Printf("error while registering comments of user %s: %v", user.Name, err)
			}

			if user.Suspended || user.NotFound {
				if rs.suspensions != nil {
					rs.suspensions <- user
				}
				break
			}

			if err := rs.alertIfHighScore(comments); err != nil {
				rs.logger.Print(err)
			}

			if user.Position == "" {
				break
			}
		}

	}
	return nil
}

func (rs *RedditScanner) getUsersOrWait(ctx context.Context, full_scan bool) []User {
	var users []User
	// We could be using a channel to signal when a new user is added,
	// but this isn't worth complicating AddUser for a feature that
	// is used in production only once, when the database is empty.
	for sleepCtx(ctx, time.Second) {
		if full_scan {
			users = rs.storage.ListUsers()
		} else {
			users = rs.storage.ListActiveUsers()
		}
		if len(users) > 0 {
			break
		}
	}
	return users
}

func (rs *RedditScanner) alertIfHighScore(comments []Comment) error {
	if rs.highScores == nil {
		return nil
	}

	for _, comment := range comments {

		if comment.Score < rs.highScoreThreshold {

			if rs.storage.IsKnownObject(comment.Id) {
				continue
			}

			if err := rs.storage.SaveKnownObject(comment.Id); err != nil {
				return err
			}

			rs.highScores <- comment

		}

	}

	return nil
}
