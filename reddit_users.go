package main

import (
	"context"
	"fmt"
	"time"
)

// Component
type RedditUsers struct {
	api     RedditUsersAPI
	logger  LevelLogger
	storage RedditUsersStorage

	unsuspensions              chan User
	unsuspensionInterval       time.Duration
	UnsuspensionWatcherEnabled bool
}

func NewRedditUsers(
	logger LevelLogger,
	storage RedditUsersStorage,
	api RedditUsersAPI,
	conf RedditUsersConf,
) *RedditUsers {
	return &RedditUsers{
		api:     api,
		logger:  logger,
		storage: storage,

		unsuspensions:              make(chan User, DefaultChannelSize),
		unsuspensionInterval:       conf.UnsuspensionInterval.Value,
		UnsuspensionWatcherEnabled: conf.UnsuspensionInterval.Value > 0,
	}
}

func (ru *RedditUsers) AddUserServer(ctx context.Context, queries chan UserQuery) error {
	ru.logger.Info("starting internal server to register users")
	var query UserQuery
Loop:
	for {
		select {
		case query = <-queries:
			break
		case <-ctx.Done():
			break Loop
		}
		ru.logger.Infof("received query to add a new user, %+v", query)
		query = ru.AddUser(ctx, query.User.Name, query.User.Hidden, false)
		ru.logger.Infof("replying to query to add a new user, %+v", query)
		select {
		case queries <- query:
			break
		case <-ctx.Done():
			break Loop
		}
	}
	return ctx.Err()
}

func (ru *RedditUsers) AddUser(ctx context.Context, username string, hidden bool, force_suspended bool) UserQuery {
	query := UserQuery{User: User{Name: username}}

	query = ru.storage.GetUser(ctx, username)
	if query.Error != nil {
		return query
	} else if query.Exists {
		query.Error = fmt.Errorf("%q already exists", username)
		ru.logger.Error(query.Error)
		return query
	}

	query = ru.api.AboutUser(ctx, username)
	if query.Error != nil || !query.Exists {
		return query
	}

	if query.User.Suspended {
		if !force_suspended {
			query.Error = fmt.Errorf("user %q can't be added, forced mode not enabled", query.User.Name)
			return query
		}
	}

	if err := ru.storage.AddUser(ctx, query.User.Name, hidden, query.User.Created); err != nil {
		query.Error = err
	}

	if query.User.Suspended {
		if err := ru.storage.SuspendUser(ctx, query.User.Name); err != nil {
			query.Error = err
		}
	}

	return query
}

func (ru *RedditUsers) Unsuspensions() <-chan User {
	return ru.unsuspensions
}

func (ru *RedditUsers) UnsuspensionWatcher(ctx context.Context) error {
	ru.logger.Infof("watching unsuspensions/undeletions with interval %s", ru.unsuspensionInterval)
	for SleepCtx(ctx, ru.unsuspensionInterval) {
		ru.logger.Debug("checking uspended/deleted users")

		users, err := ru.storage.ListSuspendedAndNotFound(ctx)
		if err != nil {
			return err
		}
		for _, user := range users {
			ru.logger.Debugf("checking suspended/deleted user %+v", user)

			res := ru.api.AboutUser(ctx, user.Name)
			if IsCancellation(res.Error) {
				return res.Error
			}
			if res.Error != nil {
				ru.logger.Error(res.Error)
				continue
			}

			/* Actions depending in change of status (from is "user", to is "res"):

			 from \ to | Alive  | Suspended | Deleted
			-----------|------------------------------
			 Alive     | NA     | NA        | NA
			------------------------------------------
			 Suspended | signal | ignore    | update
			------------------------------------------
			 Deleted   | signal | update    | ignore

			ignore: don't signal, don't update
			signal: update the database and signal the change
			update: update the database, don't signal the change
			NA: not applicable (we only have suspended or deleted users to begin with)

			*/

			if user.NotFound && res.Exists { // undeletion
				if err := ru.storage.FoundUser(ctx, user.Name); err != nil {
					ru.logger.Error(err)
					continue
				}
				if res.User.Suspended {
					if err := ru.storage.SuspendUser(ctx, user.Name); err != nil {
						ru.logger.Error(err)
					}
					continue // don't signal accounts that went from deleted to suspended
				}
			} else if user.Suspended && !res.Exists { // deletion of a suspended account
				if err := ru.storage.NotFoundUser(ctx, user.Name); err != nil {
					ru.logger.Error(err)
					continue
				}
				continue // don't signal it, we only need to keep track of it
			} else if user.Suspended && !res.User.Suspended { // unsuspension
				if err := ru.storage.UnSuspendUser(ctx, user.Name); err != nil {
					ru.logger.Error(err)
					continue
				}
			} else { // no change
				continue
			}

			user.NotFound = res.Exists
			user.Suspended = res.User.Suspended
			ru.unsuspensions <- res.User

		}

	}
	close(ru.unsuspensions)
	return ctx.Err()
}
