package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

type RedditUsers struct {
	api     RedditUsersAPI
	logger  *log.Logger
	storage RedditUsersStorage

	compendiumUpdateInterval time.Duration
	unsuspensionInterval     time.Duration

	Unsuspensions chan User
}

func NewRedditUsers(
	logger *log.Logger,
	storage RedditUsersStorage,
	api RedditUsersAPI,
	conf RedditUsersConf,
) *RedditUsers {
	return &RedditUsers{
		api:     api,
		logger:  logger,
		storage: storage,

		compendiumUpdateInterval: conf.CompendiumUpdateInterval.Value,
		unsuspensionInterval:     conf.UnsuspensionInterval.Value,

		Unsuspensions: make(chan User),
	}
}

func (ru *RedditUsers) AddUserServer(ctx context.Context, queries chan UserQuery) error {
	ru.logger.Print("init addition of new users")
	for query := range queries {
		ru.logger.Print("received query to add a new user: ", query)

		query = ru.AddUser(ctx, query.User.Name, query.User.Hidden, false)
		if query.Error != nil {
			msg := "error when adding the new user "
			ru.logger.Print(msg, query.User.Name, query.Error)
		}

		queries <- query
	}
	return nil
}

func (ru *RedditUsers) AddUser(ctx context.Context, username string, hidden bool, force_suspended bool) UserQuery {
	ru.logger.Print("trying to add user ", username)
	query := UserQuery{User: User{Name: username}}

	query = ru.storage.GetUser(username)
	if query.Error != nil {
		return query
	} else if query.Exists {
		template := "'%s' already exists"
		ru.logger.Printf(template, username)
		query.Error = fmt.Errorf(template, username)
		return query
	}

	query = ru.api.AboutUser(ctx, username)
	if query.Error != nil {
		return query
	}

	if !query.Exists {
		ru.logger.Printf("user \"%s\" not found", username)
		return query
	}

	if query.User.Suspended {
		ru.logger.Printf("user \"%s\" was suspended", query.User.Name)
		if !force_suspended {
			query.Error = fmt.Errorf("user '%s' can't be added, forced mode not enabled", query.User.Name)
			return query
		}
	}

	if err := ru.storage.AddUser(query.User.Name, hidden, query.User.Created); err != nil {
		query.Error = err
		return query
	}

	ru.logger.Printf("new user \"%s\" sucessfully added", query.User.Name)
	return query
}

func (ru *RedditUsers) AutoUpdateUsersFromCompendium(ctx context.Context) error {
	for ctx.Err() == nil {
		if err := ru.UpdateUsersFromCompendium(ctx); err != nil {
			return err
		}
		if !sleepCtx(ctx, ru.compendiumUpdateInterval) {
			break
		}
	}
	return ctx.Err()
}

func (ru *RedditUsers) UpdateUsersFromCompendium(ctx context.Context) error {
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
		if isCancellation(result.Error) {
			return result.Error
		} else if result.Error != nil {
			if !result.Exists {
				msg := "update from compendium: error when adding the new user "
				ru.logger.Print(msg, result.User.Name, result.Error)
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

	if added_counter > 0 {
		ru.logger.Printf("found %d user(s) in the compendium, added %d new one(s)", len(names), added_counter)
	}

	return nil
}

func (ru *RedditUsers) CheckUnsuspendedAndNotFound(ctx context.Context) error {
	for ctx.Err() == nil {

		if !sleepCtx(ctx, ru.unsuspensionInterval) {
			break
		}

		for _, user := range ru.storage.ListSuspendedAndNotFound() {

			res := ru.api.AboutUser(ctx, user.Name)
			if isCancellation(res.Error) {
				return res.Error
			}
			if res.Error != nil {
				ru.logger.Print(res.Error)
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
					ru.logger.Print(err)
					continue
				}
				if res.User.Suspended {
					if err := ru.storage.SuspendUser(user.Name); err != nil {
						ru.logger.Print(err)
					}
					continue // don't signal accounts that went from deleted to suspended
				}
			} else if user.Suspended && !res.Exists { // deletion of a suspended account
				if err := ru.storage.NotFoundUser(user.Name); err != nil {
					ru.logger.Print(err)
					continue
				}
				continue // don't signal it, we only need to keep track of it
			} else if user.Suspended && !res.User.Suspended { // unsuspension
				if err := ru.storage.UnSuspendUser(user.Name); err != nil {
					ru.logger.Print(err)
					continue
				}
			} else { // no change
				continue
			}

			user.NotFound = res.Exists
			user.Suspended = res.User.Suspended
			ru.Unsuspensions <- res.User

		}

	}
	return ctx.Err()
}
