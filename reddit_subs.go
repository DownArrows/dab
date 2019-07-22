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

	target_key := "watcher_" + target

	for SleepCtx(ctx, sleep) {
		rs.logger.Debugf("checking %q for new posts", target)

		posts, _, err := fetcher(ctx, target[3:], "")
		if IsCancellation(err) {
			return err
		} else if err != nil {
			rs.logger.Errorf("error when watching %q: %v", target, err)
		}

		new_posts := make([]Comment, 0, len(posts))
		new_posts_id := make([]string, 0, len(posts))
		for _, post := range posts {
			if !rs.storage.KV().Has(target_key, post.Id) {
				new_posts = append(new_posts, post)
				new_posts_id = append(new_posts_id, post.Id)
			}
		}

		if err := rs.storage.KV().SaveMany(ctx, target_key, new_posts_id); err != nil {
			rs.logger.Errorf("error when watching %q: %v", target, err)
		}

		if rs.storage.KV().Has("watcher-seen", target) {
			for _, post := range new_posts {
				ch <- post
			}
		} else {
			if err := rs.storage.KV().Save(ctx, "watcher-seen", target); err != nil {
				return err
			}
		}

	}
	return ctx.Err()
}
