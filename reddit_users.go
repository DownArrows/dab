package main

import (
	"context"
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"time"
)

// AddRedditUser is a function for when the only thing needed is to add users by checking through Reddit first.
type AddRedditUser func(context.Context, string, bool, bool) UserQuery

// RedditUsers is a data structure to manage Reddit users by interacting with both the database and Reddit.
type RedditUsers struct {
	api     RedditUsersAPI
	logger  LevelLogger
	storage RedditUsersStorage

	unsuspensions              chan User
	unsuspensionInterval       time.Duration
	UnsuspensionWatcherEnabled bool
}

// NewRedditUsers creates a RedditUsers.
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

// Add registers the a user, sets it to "hidden" or not,
// and with the argument forceSuspended can add the user even if it was found to be suspended.
// Case-insensitive.
func (ru *RedditUsers) Add(ctx context.Context, username string, hidden bool, forceSuspended bool) UserQuery {
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
		if !forceSuspended {
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

// OpenUnsuspensions returns a channel that alerts of newly unsuspended or undeleted users.
func (ru *RedditUsers) OpenUnsuspensions() <-chan User {
	return ru.unsuspensions
}

// CloseUnsuspensions closes the channel that signals unsuspended or undeleted users.
func (ru *RedditUsers) CloseUnsuspensions() {
	close(ru.unsuspensions)
}

// UnsuspensionWatcher is a Task to be launched independently that watches unsuspensions
// and send the unsuspended users User to the channel returned by Unsuspensions.
func (ru *RedditUsers) UnsuspensionWatcher(ctx context.Context) error {
	ru.logger.Infof("watching unsuspensions/undeletions with interval %s", ru.unsuspensionInterval)
	for SleepCtx(ctx, ru.unsuspensionInterval) {
		ru.logger.Debug("checking uspended/deleted users")

		users, err := ru.storage.ListSuspendedAndNotFound(ctx)
		// necessary since the database layer isn't very reliable yet ("database locked" errors)
		// TODO change to a simple return once this is fixed
		if sqliteErr, ok := err.(*sqlite.Error); ok && sqliteErr != nil {
			ru.logger.Errorf("unsuspensions/undeletions watcher database error: %v", sqliteErr)
		} else if err != nil {
			return err
		}

		for _, user := range users {
			ru.logger.Debugf("checking suspended/deleted user %+v", user)

			res := ru.api.AboutUser(ctx, user.Name)
			if res.Error != nil {
				return res.Error
			}

			ru.logger.Debugf("unsuspensions/undeletions watcher found about user %+v data from Reddit %+v", user, res)

			if err := ru.updateRedditUserStatus(ctx, user, res); err != nil {
				// necessary since the database layer isn't very reliable yet ("database locked" errors)
				// TODO change to a simple return once this is fixed
				if sqliteErr, ok := err.(*sqlite.Error); ok && sqliteErr != nil {
					ru.logger.Errorf("unsuspensions/undeletions watcher database error: %v", sqliteErr)
				} else {
					return err
				}
			}

		}
	}
	return ctx.Err()
}

func (ru *RedditUsers) updateRedditUserStatus(ctx context.Context, user User, res UserQuery) error {
	/* Actions depending in change of status (from is "user", to is "res"):

	 from \ to | Alive  | Suspended | Deleted
	-----------|------------------------------
	 Alive     | NA     | NA        | NA
	------------------------------------------
	 Suspended | signal | ignore    | update
	------------------------------------------
	 Deleted   | signal | update    | ignore

	ignore: don't signal, don't update
	signal: update the database and signal the change
	update: update the database, don't signal the change
	NA: not applicable (we only have suspended or deleted users to begin with)

	*/

	if user.NotFound && res.Exists { // undeletion
		if err := ru.storage.FoundUser(ctx, user.Name); err != nil {
			return err
		}
		if res.User.Suspended {
			// don't signal accounts that went from deleted to suspended
			return ru.storage.SuspendUser(ctx, user.Name)
		}
	} else if user.Suspended && !res.Exists { // deletion of a suspended account
		// if the user was already found not to exist anymore, don't pointlessly update
		if user.NotFound { // leave that condition here so as not to break the overall logic of if/else if
			return nil
		}
		// don't signal it, we only need to keep track of it
		return ru.storage.NotFoundUser(ctx, user.Name)
	} else if user.Suspended && !res.User.Suspended { // unsuspension
		if err := ru.storage.UnSuspendUser(ctx, user.Name); err != nil {
			return err
		}
	} else { // no change
		return nil
	}

	user.NotFound = res.Exists
	user.Suspended = res.User.Suspended
	ru.unsuspensions <- res.User

	return nil
}
