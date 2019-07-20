package main

import (
	"context"
	"fmt"
	"time"
)

type WatchSubmissions struct {
	Target   string
	Interval Duration
}

// Component
type RedditSubs struct {
	api     RedditSubsAPI
	logger  LevelLogger
	storage RedditSubsStorage
}

func NewRedditSubs(logger LevelLogger, storage RedditSubsStorage, api RedditSubsAPI) *RedditSubs {
	return &RedditSubs{
		api:     api,
		logger:  logger,
		storage: storage,
	}
}

func (rs *RedditSubs) WatchSubmissions(ctx context.Context, config WatchSubmissions, ch chan<- Comment) error {
	var fetcher func(context.Context, string, string) ([]Comment, string, error)
	target := config.Target
	sleep := config.Interval.Value

	if sleep < time.Minute {
		return fmt.Errorf("watcher interval for %q must be greater than a minute", target)
	}

	if prefix := target[:3]; prefix == "/r/" {
		fetcher = rs.api.SubPosts
	} else if prefix == "/u/" {
		fetcher = rs.api.UserSubmissions
	} else {
		return fmt.Errorf("the target of the submission watcher must start with /r/ or /u/, not %q", prefix)
	}

	rs.logger.Infof("watching new posts from %q with interval %s", target, sleep)

	for SleepCtx(ctx, sleep) {
		rs.logger.Debugf("checking %q for new posts", target)

		posts, _, err := fetcher(ctx, target[3:], "")
		if IsCancellation(err) {
			return err
		} else if err != nil {
			rs.logger.Errorf("error when watching %q: %v", target, err)
		}

		new_posts := make([]Comment, 0, len(posts))
		for _, post := range posts {
			if !rs.storage.IsKnownSubPostID(target, post.Id) {
				new_posts = append(new_posts, post)
			}
		}

		if err := rs.storage.SaveSubPostIDs(target, posts); err != nil {
			rs.logger.Errorf("error when watching %q: %v", target, err)
		}

		if !rs.storage.IsKnownObject("submissions-from-" + target) {
			rs.storage.SaveKnownObject("submissions-from-" + target)
			continue
		}

		for _, post := range new_posts {
			ch <- post
		}

	}
	return ctx.Err()
}
