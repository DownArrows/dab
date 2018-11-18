package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

const MaxRedditListingLength = 100

const nbCommentsLeeway = 5

type RedditBot struct {
	Conf           RedditBotConf
	Suspended      chan User
	highScoresFeed chan Comment
	highScore      int64
	scanner        *Scanner
	storage        RedditBotStorage
	logger         *log.Logger
}

func NewRedditBot(scanner *Scanner, storage RedditBotStorage, logger *log.Logger, conf RedditBotConf) *RedditBot {
	return &RedditBot{
		scanner: scanner,
		storage: storage,
		logger:  logger,
		Conf:    conf,
	}
}

func (bot *RedditBot) AutoCompendiumUpdate(interval time.Duration) {
	if interval == 0*time.Second {
		bot.logger.Print("interval for auto-update from DVT's compendium is 0s, disabling")
		return
	}
	for {
		time.Sleep(interval)
		if err := bot.UpdateUsersFromCompendium(); err != nil {
			bot.logger.Print(err)
		}
	}
}

func (bot *RedditBot) UpdateUsersFromCompendium() error {
	page, err := bot.scanner.WikiPage("downvote_trolls", "compendium")
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

		if bot.storage.IsKnownObject("username-" + username) {
			continue
		}

		result := bot.AddUser(username, false, false)
		if result.Error != nil {
			if !result.Exists {
				msg := "update from compendium: error when adding the new user "
				bot.logger.Print(msg, result.User.Name, result.Error)
			}
		} else if result.Exists {
			added_counter += 1
		}

		if result.Error != nil || !result.Exists {
			if err := bot.storage.SaveKnownObject("username-" + username); err != nil {
				return err
			}
		}

	}

	if added_counter > 0 {
		bot.logger.Printf("found %d user(s) in the compendium, added %d new one(s)", len(names), added_counter)
	}

	return nil
}

func (bot *RedditBot) StreamSub(sub string, ch chan Comment, sleep time.Duration) {
	bot.logger.Print("streaming new posts from ", sub)

	// This assumes the sub isn't empty
	first_time := (bot.storage.NbKnownPostIDs(sub) == 0)

	for {
		posts, _, err := bot.scanner.SubPosts(sub, "")
		if err != nil {
			bot.logger.Print("event streamer: ", err)
		}

		new_posts := make([]Comment, 0, len(posts))
		for _, post := range posts {
			if !bot.storage.IsKnownSubPostID(sub, post.Id) {
				new_posts = append(new_posts, post)
			}
		}

		if err := bot.storage.SaveSubPostIDs(sub, posts); err != nil {
			bot.logger.Print("event streamer: ", err)
		}

		if first_time {
			first_time = false
			break
		}

		for _, post := range new_posts {
			ch <- post
		}

		time.Sleep(sleep)
	}
}

func (bot *RedditBot) AddUserServer(queries chan UserQuery) {
	bot.logger.Print("init addition of new users")
	for query := range queries {
		bot.logger.Print("received query to add a new user: ", query)

		query = bot.AddUser(query.User.Name, query.User.Hidden, false)
		if query.Error != nil {
			msg := "error when adding the new user "
			bot.logger.Print(msg, query.User.Name, query.Error)
		}

		queries <- query
	}
}

// This function mutates the bot struct, there is no locking,
// so use this function before the bot runs.
func (bot *RedditBot) StartHighScoresFeed(threshold int64) chan Comment {
	bot.highScore = threshold
	bot.highScoresFeed = make(chan Comment)
	return bot.highScoresFeed
}

func (bot *RedditBot) AddUser(username string, hidden bool, force_suspended bool) UserQuery {
	bot.logger.Print("trying to add user ", username)
	query := UserQuery{User: User{Name: username}}

	query = bot.storage.GetUser(username)
	if query.Error != nil {
		return query
	} else if query.Exists {
		template := "'%s' already exists"
		bot.logger.Printf(template, username)
		query.Error = fmt.Errorf(template, username)
		return query
	}

	query = bot.scanner.AboutUser(username)
	if query.Error != nil {
		return query
	}

	if !query.Exists {
		bot.logger.Printf("user \"%s\" not found", username)
		return query
	}

	if query.User.Suspended {
		bot.logger.Printf("user \"%s\" was suspended", query.User.Name)
		if !force_suspended {
			query.Error = fmt.Errorf("user '%s' can't be added, forced mode not enabled", query.User.Name)
			return query
		}
	}

	if err := bot.storage.AddUser(query.User.Name, hidden, query.User.Created); err != nil {
		query.Error = err
		return query
	}

	bot.logger.Printf("new user \"%s\" sucessfully added", query.User.Name)
	return query
}

func (bot *RedditBot) Suspensions() chan User {
	if bot.Suspended == nil {
		bot.Suspended = make(chan User)
	}
	return bot.Suspended
}

func (bot *RedditBot) CheckUnsuspendedAndNotFound(delay time.Duration) chan User {
	ch := make(chan User)

	go func() {

		for {

			time.Sleep(delay)

			for _, user := range bot.storage.ListSuspendedAndNotFound() {

				res := bot.scanner.AboutUser(user.Name)
				if res.Error != nil {
					bot.logger.Print(res.Error)
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
					if err := bot.storage.FoundUser(user.Name); err != nil {
						bot.logger.Print(err)
						continue
					}
					if res.User.Suspended {
						if err := bot.storage.SuspendUser(user.Name); err != nil {
							bot.logger.Print(err)
						}
						continue // don't signal accounts that went from deleted to suspended
					}
				} else if user.Suspended && !res.Exists { // deletion of a suspended account
					if err := bot.storage.NotFoundUser(user.Name); err != nil {
						bot.logger.Print(err)
						continue
					}
					continue // don't signal it, we only need to keep track of it
				} else if user.Suspended && !res.User.Suspended { // unsuspension
					if err := bot.storage.UnSuspendUser(user.Name); err != nil {
						bot.logger.Print(err)
						continue
					}
				} else { // no change
					continue
				}

				user.NotFound = res.Exists
				user.Suspended = res.User.Suspended
				ch <- res.User

			}

		}

	}()

	return ch
}

func (bot *RedditBot) Run() {
	// Band-aid until a proper closing sequence is written.
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok {
				bot.logger.Print(err)
			}
		}
	}()

	var last_full_scan time.Time

	for {

		now := time.Now().Round(0)
		full_scan := now.Sub(last_full_scan) >= bot.Conf.FullScanInterval.Value
		users := bot.getUsersOrWait(full_scan)

		bot.scan(users)

		if full_scan {
			last_full_scan = now
			if err := bot.storage.UpdateInactiveStatus(bot.Conf.InactivityThreshold.Value); err != nil {
				bot.logger.Fatal(err)
			}
		}

	}

}

func (bot *RedditBot) scan(users []User) {
	for _, user := range users {

		for i := uint(0); i < bot.Conf.MaxBatches; i++ {
			var err error
			var comments []Comment

			var limit uint
			if user.New || user.Position != "" || user.BatchSize+nbCommentsLeeway > MaxRedditListingLength {
				limit = MaxRedditListingLength
			} else {
				limit = user.BatchSize + nbCommentsLeeway
			}

			comments, user, err = bot.scanner.UserComments(user, limit)
			if err != nil {
				bot.logger.Printf("error while scanning user %s: %v", user.Name, err)
			}

			user, err = bot.storage.SaveCommentsUpdateUser(comments, user, bot.Conf.MaxAge.Value)
			if err != nil {
				bot.logger.Printf("error while registering comments of user %s: %v", user.Name, err)
			}

			if user.Suspended || user.NotFound {
				if bot.Suspended != nil {
					bot.Suspended <- user
				}
				break
			}

			if err := bot.AlertIfHighScore(comments); err != nil {
				bot.logger.Print(err)
			}

			if user.Position == "" {
				break
			}
		}

	}
}

func (bot *RedditBot) getUsersOrWait(full_scan bool) []User {
	var users []User

	for {

		if full_scan {
			users = bot.storage.ListUsers()
		} else {
			users = bot.storage.ListActiveUsers()
		}

		if len(users) > 0 {
			break
		}
		// We could be using a channel to signal when a new user is added,
		// but this isn't worth complicating AddUser for a feature that
		// is used in production only once, when the database is empty.
		time.Sleep(time.Second)
	}
	return users
}

func (bot *RedditBot) AlertIfHighScore(comments []Comment) error {
	if bot.highScoresFeed == nil {
		return nil
	}

	for _, comment := range comments {

		if comment.Score < bot.highScore {

			if bot.storage.IsKnownObject(comment.Id) {
				continue
			}

			if err := bot.storage.SaveKnownObject(comment.Id); err != nil {
				return err
			}

			bot.highScoresFeed <- comment

		}

	}

	return nil
}
