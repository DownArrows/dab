package main

import (
	"context"
	"log"
	"time"
)

type RedditSubs struct {
	api     RedditSubsAPI
	logger  *log.Logger
	storage RedditSubsStorage
}

func NewRedditSubs(logger *log.Logger, storage RedditSubsStorage, api RedditSubsAPI) *RedditSubs {
	return &RedditSubs{
		api:     api,
		logger:  logger,
		storage: storage,
	}
}

func (rs *RedditSubs) NewPostsOnSub(ctx context.Context, sub string, ch chan<- Comment, sleep time.Duration) error {
	rs.logger.Printf("watching new posts from %s with interval %s", sub, sleep)

	// This assumes the sub isn't empty
	first_time := (rs.storage.NbKnownPostIDs(sub) == 0)

	for SleepCtx(ctx, sleep) {

		posts, _, err := rs.api.SubPosts(ctx, sub, "")
		if IsCancellation(err) {
			return err
		} else if err != nil {
			rs.logger.Printf("error when watching sub %s: %v", sub, err)
		}

		new_posts := make([]Comment, 0, len(posts))
		for _, post := range posts {
			if !rs.storage.IsKnownSubPostID(sub, post.Id) {
				new_posts = append(new_posts, post)
			}
		}

		if err := rs.storage.SaveSubPostIDs(sub, posts); err != nil {
			rs.logger.Printf("error when watching sub %s: %v", sub, err)
		}

		if first_time {
			first_time = false
			break
		}

		for _, post := range new_posts {
			ch <- post
		}

	}
	return ctx.Err()
}
