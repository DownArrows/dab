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

func (rs *RedditSubs) StreamSub(ctx context.Context, sub string, ch chan Comment, sleep time.Duration) {
	rs.logger.Print("streaming new posts from ", sub)

	// This assumes the sub isn't empty
	first_time := (rs.storage.NbKnownPostIDs(sub) == 0)

	for ctx.Err() == nil {
		posts, _, err := rs.api.SubPosts(ctx, sub, "")
		if err != nil {
			if isContextError(err) {
				return
			}
			rs.logger.Print("event streamer: ", err)
		}

		new_posts := make([]Comment, 0, len(posts))
		for _, post := range posts {
			if !rs.storage.IsKnownSubPostID(sub, post.Id) {
				new_posts = append(new_posts, post)
			}
		}

		if err := rs.storage.SaveSubPostIDs(sub, posts); err != nil {
			rs.logger.Print("event streamer: ", err)
		}

		if first_time {
			first_time = false
			break
		}

		for _, post := range new_posts {
			ch <- post
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
			break
		}
	}
}
