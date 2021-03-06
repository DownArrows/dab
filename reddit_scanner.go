package main

import (
	"context"
	"sync"
	"time"
)

// RedditScanner is a component that efficiently scans users' comments and saves them.
type RedditScanner struct {
	// dependencies
	api     *RedditAPI
	logger  LevelLogger
	storage *Storage

	// communication with the outside
	sync.Mutex
	deaths     chan User
	highScores chan Comment

	// configuration
	fullScanInterval    time.Duration
	highScoreThreshold  int64
	inactivityThreshold time.Duration
	maxAge              time.Duration
	maxBatches          uint
	commentsLeeway      uint
}

// NewRedditScanner creates a new RedditScanner.
func NewRedditScanner(logger LevelLogger, storage *Storage, api *RedditAPI, conf RedditScannerConf) *RedditScanner {
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

// Run launches the scanner and blocks until it errors out or is cancelled.
// Note that network errors are only logged and not returned, as Reddit is rather unreliable.
func (rs *RedditScanner) Run(ctx context.Context) error {
	var lastFullScan time.Time

	conn, err := rs.storage.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	rs.logger.Info("starting comments scanner")

	for ctx.Err() == nil {

		fullScan := time.Now().Sub(lastFullScan) >= rs.fullScanInterval

		users, err := rs.getUsersOrWait(ctx, conn, fullScan)
		if err != nil {
			return err
		}

		rs.logger.Debugf("scan pass: %d users, full scan: %t", len(users), fullScan)
		if err := rs.Scan(ctx, conn, users); err != nil {
			return err
		}
		rs.logger.Debug("scan pass done")

		if fullScan {
			lastFullScan = time.Now()
			if err := conn.UpdateInactiveStatus(rs.inactivityThreshold); err != nil {
				return err
			}
		}

		if err := conn.Analyze(); err != nil {
			return err
		}
	}

	rs.logger.Debug("scanner run done")

	return ctx.Err()
}

// OpenDeaths creates, set, and returns a channel that sends newly suspended or deleted User.
func (rs *RedditScanner) OpenDeaths() <-chan User {
	rs.Lock()
	defer rs.Unlock()
	if rs.deaths == nil {
		rs.deaths = make(chan User, DefaultChannelSize)
	}
	return rs.deaths
}

// CloseDeaths closes and unsets the channel that sends suspended and deleted User.
func (rs *RedditScanner) CloseDeaths() {
	rs.Lock()
	defer rs.Unlock()
	if rs.deaths != nil {
		close(rs.deaths)
		rs.deaths = nil
	}
}

// OpenHighScores creates, set, and returns a channel that sends comments whose score just passed a set threshold.
func (rs *RedditScanner) OpenHighScores() <-chan Comment {
	rs.Lock()
	defer rs.Unlock()
	if rs.highScores == nil {
		rs.highScores = make(chan Comment, DefaultChannelSize)
	}
	return rs.highScores
}

// CloseHighScores closes and unsets the channel that sends high scoring comments.
func (rs *RedditScanner) CloseHighScores() {
	rs.Lock()
	defer rs.Unlock()
	if rs.highScores != nil {
		close(rs.highScores)
		rs.highScores = nil
	}
}

// Scan scans a slice of users once.
func (rs *RedditScanner) Scan(ctx context.Context, conn StorageConn, users []User) error {
OUTER:
	for _, user := range users {

		for i := uint(0); i < rs.maxBatches; i++ {
			var err error
			var comments []Comment
			var limit uint
			lastScan := time.Now().Sub(user.LastScan)

			if user.New || // if the user is new, we need to scan everything as fast as possible
				user.Position != "" || // we don't know how many relevant comments the next page has, so take as many as possible
				user.BatchSize+rs.commentsLeeway > MaxRedditListingLength || // don't request more than the maximum, else we'll look stupid
				lastScan > rs.maxAge { // use rs.maxAge as a heuristic to say if too much time has passed since the last scan
				limit = MaxRedditListingLength
			} else {
				limit = user.BatchSize + rs.commentsLeeway
			}

			rs.logger.Debugf("trying to get %d comments from user %+v", limit, user)
			comments, user, err = rs.api.UserComments(ctx, user, limit)
			if IsCancellation(err) {
				return err
			} else if err != nil {
				rs.logger.Errorf("error while scanning user %q, skipping: %v", user.Name, err)
				continue OUTER
			}
			rs.logger.Debugf("fetched comments: %+v", comments)

			rs.logger.Debugf("before scanner's user update: %+v", user)
			// This method contains logic that returns an User datastructure whose metadata
			// has been updated; in other words, it indirectly controls the behavior of the
			// current loop.
			user, err = conn.SaveCommentsUpdateUser(comments, user, lastScan+rs.maxAge)
			if err != nil {
				if IsSQLiteForeignKeyErr(err) { // triggered after a PurgeUser
					rs.logger.Debugf("saving the comments of %q resulted in a foreign key constraint error, skipping", user.Name)
					continue OUTER
				}
				return err
			}
			rs.logger.Debugf("after scanner's user update: %+v", user)

			if user.Suspended || user.NotFound {
				rs.Lock()
				if rs.deaths != nil {
					rs.deaths <- user
				}
				rs.Unlock()
				break
			}

			if err := rs.alertIfHighScore(conn, comments); err != nil {
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

func (rs *RedditScanner) getUsersOrWait(ctx context.Context, conn StorageConn, fullScan bool) ([]User, error) {
	var users []User
	var err error
	// We could be using a channel to signal when a new user is added,
	// but this isn't worth complicating AddUser for a feature that
	// is used in production only once, when the database is empty.
	for SleepCtx(ctx, time.Second) {
		if fullScan {
			users, err = conn.ListUsers()
			if err != nil {
				return nil, err
			}
		} else {
			users, err = conn.ListActiveUsers()
			if err != nil {
				return nil, err
			}
		}
		if len(users) > 0 {
			break
		}
	}
	return users, nil
}

func (rs *RedditScanner) alertIfHighScore(conn StorageConn, comments []Comment) error {
	rs.Lock()
	defer rs.Unlock()

	if rs.highScores == nil {
		return nil
	}

	var highscoresID []string
	var highscores []Comment
	for _, comment := range comments {
		if comment.Score < rs.highScoreThreshold {
			if !rs.storage.KV().Has("highscores", comment.ID) {
				highscoresID = append(highscoresID, comment.ID)
				highscores = append(highscores, comment)
			}
		}
	}

	err := conn.WithTx(func() error { return rs.storage.KV().SaveMany(conn, "highscores", highscoresID) })
	if err != nil {
		return err
	}

	for _, comment := range highscores {
		rs.highScores <- comment
	}

	return nil
}
