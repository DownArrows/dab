package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type RedditUsers struct {
	api     RedditUsersAPI
	logger  LevelLogger
	storage RedditUsersStorage

	compendiumUpdateInterval             time.Duration
	AutoUpdateUsersFromCompendiumEnabled bool

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

		compendiumUpdateInterval:             conf.CompendiumUpdateInterval.Value,
		AutoUpdateUsersFromCompendiumEnabled: conf.CompendiumUpdateInterval.Value > 0,

		unsuspensions:              make(chan User),
		unsuspensionInterval:       conf.UnsuspensionInterval.Value,
		UnsuspensionWatcherEnabled: conf.UnsuspensionInterval.Value > 0,
	}
}

func (ru *RedditUsers) AddUserServer(ctx context.Context, queries chan UserQuery) error {
	ru.logger.Info("starting internal server to register users")
Loop:
	for {
		select {
		case <-ctx.Done():
			break Loop
		case query := <-queries:
			ru.logger.Infof("received query to add a new user, %s", query)
			query = ru.AddUser(ctx, query.User.Name, query.User.Hidden, false)
			ru.logger.Infof("replying to query to add a new user, %s", query)
			queries <- query
		}
	}
	return ctx.Err()
}

func (ru *RedditUsers) AddUser(ctx context.Context, username string, hidden bool, force_suspended bool) UserQuery {
	query := UserQuery{User: User{Name: username}}

	query = ru.storage.GetUser(username)
	if query.Error != nil {
		return query
	} else if query.Exists {
		template := "'%s' already exists"
		ru.logger.Errorf(template, username)
		query.Error = fmt.Errorf(template, username)
		return query
	}

	query = ru.api.AboutUser(ctx, username)
	if query.Error != nil {
		return query
	}

	if !query.Exists {
		return query
	}

	if query.User.Suspended {
		if !force_suspended {
			query.Error = fmt.Errorf("user '%s' can't be added, forced mode not enabled", query.User.Name)
			return query
		}
	}

	if err := ru.storage.AddUser(query.User.Name, hidden, query.User.Created); err != nil {
		query.Error = err
	}

	return query
}

func (ru *RedditUsers) AutoUpdateUsersFromCompendium(ctx context.Context) error {
	ru.logger.Infof("updating users from the compendium with interval %s", ru.compendiumUpdateInterval)
	for SleepCtx(ctx, ru.compendiumUpdateInterval) {
		if err := ru.UpdateUsersFromCompendium(ctx); err != nil {
			ru.logger.Errorf("user list updater: %v", err)
		}
	}
	return ctx.Err()
}

func (ru *RedditUsers) UpdateUsersFromCompendium(ctx context.Context) error {
	ru.logger.Debug("updating users from compendium")
	page, err := ru.api.WikiPage(ctx, "downvote_trolls", "compendium")
	if err != nil {
		return err
	}

	lines := strings.Split(page, "\n")
	state := "before"
	names := make([]string, 0)

	for _, line := range lines {
		if line == "####Users ranked by total comment karma" {
			state = "header1"
		} else if state == "header1" {
			state = "header2"
		} else if state == "header2" {
			state = "in_listing"
		} else if state == "in_listing" && strings.HasPrefix(line, "*") && strings.HasSuffix(line, "*") {
			break
		} else if state == "in_listing" {
			cells := strings.Split(line, "|")
			if len(cells) < 6 {
				return fmt.Errorf("the array of names/scores doesn't have the expected format")
			}
			name_link := cells[2]
			start := strings.Index(name_link, "[")
			end := strings.Index(name_link, "]")
			escaped_name := name_link[start+1 : end]
			if len(escaped_name) == 0 {
				return fmt.Errorf("the names don't have the expected format")
			}
			name := strings.Replace(escaped_name, `\`, "", -1)
			names = append(names, name)
		}
	}

	added_counter := 0
	for _, username := range names {

		if ru.storage.IsKnownObject("username-" + username) {
			continue
		}

		result := ru.AddUser(ctx, username, false, false)
		if IsCancellation(result.Error) {
			return result.Error
		} else if result.Error != nil {
			if !result.Exists {
				msg := "update from compendium: error when adding the new user %s: %v"
				ru.logger.Errorf(msg, result.User.Name, result.Error)
			}
		} else if result.Exists {
			added_counter += 1
		}

		if result.Error != nil || !result.Exists {
			if err := ru.storage.SaveKnownObject("username-" + username); err != nil {
				return err
			}
		}

	}

	ru.logger.Infof("found %d user(s) in the compendium, added %d new one(s)", len(names), added_counter)

	return nil
}

func (ru *RedditUsers) Unsuspensions() <-chan User {
	return ru.unsuspensions
}

func (ru *RedditUsers) UnsuspensionWatcher(ctx context.Context) error {
	ru.logger.Infof("watching unsuspensions/undeletions with interval %s", ru.unsuspensionInterval)
	for SleepCtx(ctx, ru.unsuspensionInterval) {
		ru.logger.Debug("checking uspended/deleted users")

		for _, user := range ru.storage.ListSuspendedAndNotFound() {
			ru.logger.Debugf("checking suspended/deleted user %s", user.Name)

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
				if err := ru.storage.FoundUser(user.Name); err != nil {
					ru.logger.Error(err)
					continue
				}
				if res.User.Suspended {
					if err := ru.storage.SuspendUser(user.Name); err != nil {
						ru.logger.Error(err)
					}
					continue // don't signal accounts that went from deleted to suspended
				}
			} else if user.Suspended && !res.Exists { // deletion of a suspended account
				if err := ru.storage.NotFoundUser(user.Name); err != nil {
					ru.logger.Error(err)
					continue
				}
				continue // don't signal it, we only need to keep track of it
			} else if user.Suspended && !res.User.Suspended { // unsuspension
				if err := ru.storage.UnSuspendUser(user.Name); err != nil {
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
	return ctx.Err()
}
