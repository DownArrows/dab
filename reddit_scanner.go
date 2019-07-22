package main

import (
	"context"
	"sync"
	"time"
)

// Component
type RedditScanner struct {
	// dependencies
	api     RedditScannerAPI
	logger  LevelLogger
	storage RedditScannerStorage

	// communication with the outside
	sync.Mutex
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
	logger LevelLogger,
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

func (rs *RedditScanner) Run(ctx context.Context) error {
	var last_full_scan time.Time

	rs.logger.Info("starting comments scanner")

	for ctx.Err() == nil {

		now := time.Now().Round(0)
		full_scan := now.Sub(last_full_scan) >= rs.fullScanInterval

		users := rs.getUsersOrWait(ctx, full_scan)
		if len(users) == 0 {
			return ctx.Err()
		}

		rs.logger.Debugf("scan pass: %d users, full scan: %t", len(users), full_scan)
		if err := rs.Scan(ctx, users); err != nil {
			return err
		}
		rs.logger.Debug("scan pass done")

		if full_scan {
			last_full_scan = now
			if err := rs.storage.UpdateInactiveStatus(rs.inactivityThreshold); err != nil {
				return err
			}
		}

	}

	rs.logger.Debug("scanner run done")

	return ctx.Err()
}

func (rs *RedditScanner) OpenSuspensions() <-chan User {
	rs.Lock()
	defer rs.Unlock()
	if rs.suspensions == nil {
		rs.suspensions = make(chan User, DefaultChannelSize)
	}
	return rs.suspensions
}

func (rs *RedditScanner) CloseSuspensions() {
	rs.Lock()
	defer rs.Unlock()
	if rs.suspensions != nil {
		close(rs.suspensions)
		rs.suspensions = nil
	}
}

func (rs *RedditScanner) OpenHighScores() <-chan Comment {
	rs.Lock()
	defer rs.Unlock()
	if rs.highScores == nil {
		rs.highScores = make(chan Comment, DefaultChannelSize)
	}
	return rs.highScores
}

func (rs *RedditScanner) CloseHighScores() {
	rs.Lock()
	defer rs.Unlock()
	if rs.highScores != nil {
		close(rs.highScores)
		rs.highScores = nil
	}
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

			rs.logger.Debugf("trying to get %d comments from user %s, last scanned %v ago, page position '%s'",
				limit, user.Name, last_scan, user.Position)
			comments, user, err = rs.api.UserComments(ctx, user, limit)
			if IsCancellation(err) {
				return err
			} else if err != nil {
				rs.logger.Errorf("error while scanning user %s: %v", user.Name, err)
			}

			// This method contains logic that returns an User datastructure whose metadata
			// has been updated; in other words, it indirectly controls the behavior of the
			// current loop.
			user, err = rs.storage.SaveCommentsUpdateUser(comments, user, last_scan+rs.maxAge)
			if err != nil {
				rs.logger.Errorf("error while registering comments of user %s: %v", user.Name, err)
			}

			if user.Suspended || user.NotFound {
				rs.logger.Debugf("user %s status change: suspended %t, not found: %t", user.Name, user.Suspended, user.NotFound)
				rs.Lock()
				if rs.suspensions != nil {
					rs.suspensions <- user
				}
				rs.Unlock()
				break
			}

			if err := rs.alertIfHighScore(ctx, comments); err != nil {
				rs.logger.Error(err)
			}

			// There are no more pages to scan, either because that's what the Reddit API returned,
			// or because the logic we called previously decided no more pages should be scanned.
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
	for SleepCtx(ctx, time.Second) {
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

func (rs *RedditScanner) alertIfHighScore(ctx context.Context, comments []Comment) error {
	rs.Lock()
	defer rs.Unlock()

	if rs.highScores == nil {
		return nil
	}

	var highscores_id []string
	var highscores []Comment
	for _, comment := range comments {
		if comment.Score < rs.highScoreThreshold {
			if !rs.storage.KV().Has("highscores", comment.Id) {
				highscores_id = append(highscores_id, comment.Id)
				highscores = append(highscores, comment)
			}
		}
	}

	if err := rs.storage.KV().SaveMany(ctx, "highscores", highscores_id); err != nil {
		return err
	}

	for _, comment := range highscores {
		rs.highScores <- comment
	}

	return nil
}
