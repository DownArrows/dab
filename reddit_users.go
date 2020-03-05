package main

import (
	"fmt"
	"time"
)

// AddRedditUser is a function for when the only thing needed is to add users by checking through Reddit first.
type AddRedditUser func(Ctx, StorageConn, string, bool, bool) UserQuery

// RedditUsers is a data structure to manage Reddit users by interacting with both the database and Reddit.
type RedditUsers struct {
	api    *RedditAPI
	logger LevelLogger

	resurrections               chan User
	ResurrectionsInterval       time.Duration
	ResurrectionsWatcherEnabled bool
}

// NewRedditUsers creates a RedditUsers.
func NewRedditUsers(logger LevelLogger, api *RedditAPI, conf RedditUsersConf) *RedditUsers {
	return &RedditUsers{
		api:    api,
		logger: logger,

		resurrections:               make(chan User, DefaultChannelSize),
		ResurrectionsInterval:       conf.ResurrectionsInterval.Value,
		ResurrectionsWatcherEnabled: conf.ResurrectionsInterval.Value > 0,
	}
}

// Add registers the a user, sets it to "hidden" or not,
// and with the argument forceSuspended can add the user even if it was found to be suspended.
// Case-insensitive.
func (ru *RedditUsers) Add(ctx Ctx, conn StorageConn, username string, hidden, forceSuspended bool) UserQuery {
	query := UserQuery{User: User{Name: username}}

	query = conn.GetUser(username)
	if query.Error != nil {
		return query
	} else if query.Exists {
		query.Error = fmt.Errorf("user %q already exists", username)
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

	if err := conn.AddUser(query.User.Name, hidden, query.User.Created); err != nil {
		query.Error = err
	}

	if query.User.Suspended {
		if err := conn.SuspendUser(query.User.Name); err != nil {
			query.Error = err
		}
	}

	return query
}

// OpenResurrections returns a channel that alerts of newly unsuspended or undeleted users.
func (ru *RedditUsers) OpenResurrections() <-chan User {
	return ru.resurrections
}

// CloseResurrections closes the channel that signals unsuspended or undeleted users.
func (ru *RedditUsers) CloseResurrections() {
	close(ru.resurrections)
}

// ResurrectionsWatcher is a Task to be launched independently that watches resurrections
// and send the ressurrected Users to the channel returned by Resurrections.
func (ru *RedditUsers) ResurrectionsWatcher(ctx Ctx, conn StorageConn) error {
	ru.logger.Infof("watching resurrections with interval %s", ru.ResurrectionsInterval)

	for SleepCtx(ctx, ru.ResurrectionsInterval) {
		ru.logger.Debug("checking resurrections users")

		users, err := conn.ListSuspendedAndNotFound()
		if err != nil {
			return err
		}

		for _, user := range users {
			ru.logger.Debugf("checking resurrected user %+v", user)

			res := ru.api.AboutUser(ctx, user.Name)
			if res.Error != nil {
				if IsCancellation(res.Error) {
					return res.Error
				}
				ru.logger.Errorf("resurrections watcher network error, skipping %q: %v", user.Name, res.Error)
				continue
			}

			ru.logger.Debugf("resurrections watcher found about user %+v data from Reddit %+v", user, res)

			if err := conn.WithTx(func() error { return ru.updateRedditUserStatus(conn, user, res) }); err != nil {
				if IsSQLiteForeignKeyErr(err) { // indicates that the user has been purged
					continue
				}
				return err
			}

		}
	}
	return ctx.Err()
}

func (ru *RedditUsers) updateRedditUserStatus(conn StorageConn, user User, res UserQuery) error {
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
		if err := conn.FoundUser(user.Name); err != nil {
			return err
		}
		if res.User.Suspended {
			// don't signal accounts that went from deleted to suspended
			return conn.SuspendUser(user.Name)
		}
	} else if user.Suspended && !res.Exists { // deletion of a suspended account
		// if the user was already found not to exist anymore, don't pointlessly update
		if user.NotFound { // leave that condition here so as not to break the overall logic of if/else if
			return nil
		}
		// don't signal it, we only need to keep track of it
		return conn.NotFoundUser(user.Name)
	} else if user.Suspended && !res.User.Suspended { // unsuspension
		if err := conn.UnSuspendUser(user.Name); err != nil {
			return err
		}
	} else { // no change
		return nil
	}

	user.NotFound = res.Exists
	user.Suspended = res.User.Suspended
	ru.resurrections <- res.User

	return nil
}
