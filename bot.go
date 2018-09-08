package main

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

type BotConf struct {
	MaxAge              Duration `json:"max_age"`
	MaxBatches          uint     `json:"max_batches"`
	InactivityThreshold Duration `json:"inactivity_threshold"`
	FullScanInterval    Duration `json:"full_scan_interval"`
}

type Bot struct {
	Conf           BotConf
	Suspended      chan User
	highScoresFeed chan Comment
	highScore      int64
	scanner        *RedditClient
	storage        *Storage
	logger         *log.Logger
}

func NewBot(scanner *RedditClient, storage *Storage, logOut io.Writer, conf BotConf) *Bot {
	return &Bot{
		scanner: scanner,
		storage: storage,
		logger:  log.New(logOut, "bot: ", log.LstdFlags),
		Conf:    conf,
	}
}

func (bot *Bot) UpdateUsersFromCompendium() error {
	page, err := bot.scanner.WikiPage("DownvoteTrolling", "compendium")
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
				return fmt.Errorf("The array of names/scores doesn't have the expected format")
			}
			name_link := cells[2]
			start := strings.Index(name_link, "[")
			end := strings.Index(name_link, "]")
			escaped_name := name_link[start+1 : end]
			if len(escaped_name) == 0 {
				return fmt.Errorf("The names don't have the expected format")
			}
			name := strings.Replace(escaped_name, `\`, "", -1)
			names = append(names, name)
		}
	}

	bot.logger.Printf("Found %d users in the compendium", len(names))

	added_counter := 0
	for _, username := range names {

		is_known, err := bot.storage.IsKnownObject("username-" + username)
		if err != nil {
			return err
		}
		if is_known {
			continue
		}

		result := bot.AddUser(username, false, false)
		if result.Error != nil {
			if !result.Exists {
				msg := "Update from compendium: error when adding the new user "
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

	bot.logger.Printf("Added %d new user(s) from the compendium", added_counter)

	return nil
}

func (bot *Bot) StreamSub(sub string, ch chan Comment, sleep time.Duration) {
	bot.logger.Print("streaming new posts from ", sub)
	seen, err := bot.storage.SeenPostIDs(sub)
	if err != nil {
		bot.logger.Fatal("event streamer: ", err)
	}

	first_time := len(seen) == 0

	for {
		posts, _, err := bot.scanner.SubPosts(sub, "")
		if err != nil {
			bot.logger.Print("event streamer: ", err)
		}

		err = bot.storage.SaveSubPostIDs(posts, sub)
		if err != nil {
			bot.logger.Print("event streamer: ", err)
		}

		for _, post := range posts {
			if first_time {
				break
			}
			if !StringInSlice(post.Id, seen) {
				ch <- post
			} else {
				break
			}
		}

		ids := make([]string, 0, len(posts))
		for _, post := range posts {
			ids = append(ids, post.Id)
		}

		seen = append(seen, ids...)
		first_time = false

		time.Sleep(sleep)
	}
}

func (bot *Bot) AddUserServer(queries chan UserQuery) {
	bot.logger.Print("Init addition of new users.")
	for query := range queries {
		bot.logger.Print("Received query to add a new user: ", query)

		query = bot.AddUser(query.User.Name, query.User.Hidden, false)
		if query.Error != nil {
			msg := "Error when adding the new user "
			bot.logger.Print(msg, query.User.Name, query.Error)
		}

		queries <- query
	}
}

// This function mutates the bot struct, there is no locking,
// so use this function before the bot runs.
func (bot *Bot) StartHighScoresFeed(threshold int64) chan Comment {
	bot.highScore = threshold
	bot.highScoresFeed = make(chan Comment)
	return bot.highScoresFeed
}

func (bot *Bot) AddUser(username string, hidden bool, force_suspended bool) UserQuery {
	bot.logger.Print("Trying to add user ", username)
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
		bot.logger.Printf("User \"%s\" not found", username)
		return query
	}

	if query.User.Suspended {
		bot.logger.Printf("User \"%s\" was suspended", query.User.Name)
		if !force_suspended {
			query.Error = fmt.Errorf("User '%s' can't be added, forced mode not enabled", query.User.Name)
			return query
		}
	}

	err := bot.storage.AddUser(query.User.Name, hidden, query.User.Created)
	if err != nil {
		query.Error = err
		return query
	}

	bot.logger.Printf("New user \"%s\" sucessfully added", query.User.Name)
	return query
}

func (bot *Bot) Suspensions() chan User {
	if bot.Suspended == nil {
		bot.Suspended = make(chan User)
	}
	return bot.Suspended
}

func (bot *Bot) CheckUnsuspended(delay time.Duration) chan User {
	ch := make(chan User)

	go func() {

		for {

			time.Sleep(delay)

			suspended, err := bot.storage.ListSuspended()
			if err != nil {
				bot.logger.Print("Unsuspension checker, (re-)starting : ", err)
				continue
			}

			for _, user := range suspended {
				res := bot.scanner.AboutUser(user.Name)
				if res.Error != nil {
					bot.logger.Printf("Unsuspension checker, while checking \"%s\": %s", user.Name, res.Error)
					continue
				}

				if res.Exists && !res.User.Suspended {
					err := bot.storage.SetSuspended(user.Name, false)
					if err != nil {
						bot.logger.Printf("Unsuspension checker, while checking \"%s\": %s", user.Name, res.Error)
						continue
					}

					bot.logger.Print(user.Name, " has been unsuspended")
					ch <- user
				}
			}

		}

	}()

	return ch
}

func (bot *Bot) Run() {
	var last_full_scan time.Time

	for {

		now := time.Now().Round(0)
		full_scan := now.Sub(last_full_scan) >= bot.Conf.FullScanInterval.Value

		if err := bot.scanOnce(full_scan); err != nil {
			log.Fatal(err)
		}

		if full_scan {
			last_full_scan = now
			if err := bot.storage.UpdateInactiveStatus(bot.Conf.InactivityThreshold.Value); err != nil {
				log.Fatal(err)
			}
		}

	}

}

func (bot *Bot) scanOnce(full_scan bool) error {
	users, err := bot.getUsersOrWait(full_scan)
	if err != nil {
		return err
	}
	return bot.scanUsers(users)
}

func (bot *Bot) getUsersOrWait(full_scan bool) ([]User, error) {
	var users []User
	var err error

	for {

		if full_scan {
			users, err = bot.storage.ListUsers()
		} else {
			users, err = bot.storage.ListActiveUsers()
		}

		if err != nil {
			return nil, err
		}

		if len(users) > 0 {
			break
		}
		// We could be using a channel to signal when a new user is added,
		// but this isn't worth complicating AddUser for a feature that
		// is used in production only once, when the database is empty.
		time.Sleep(time.Second)
	}
	return users, nil
}

func (bot *Bot) scanUsers(users []User) error {
	for _, user := range users {

		err := bot.allRelevantComments(user)
		if err != nil {
			// That error is probably network-related, so just log it
			// and wait for the network or reddit to work again.
			bot.logger.Print("Error when fetching and saving comments: ", err)
		}
	}
	return nil
}

func (bot *Bot) allRelevantComments(user User) error {
	var err error
	var status int

	for i := uint(0); i < bot.Conf.MaxBatches; i++ {
		user.Position, status, err = bot.saveCommentsPage(user)
		if err != nil {
			return err
		}

		suspended, err := bot.ifSuspended(user, status)
		if err != nil {
			return err
		} else if suspended {
			break
		}

		if user.Position == "" && user.New {
			err = bot.storage.NotNewUser(user.Name)
			if err != nil {
				return err
			}
		}

		if user.Position == "" {
			err = bot.storage.ResetPosition(user.Name)
			if err != nil {
				return err
			}
			break
		}

	}

	return nil
}

func (bot *Bot) ifSuspended(user User, status int) (bool, error) {
	gone := status == 404
	forbidden := status == 403

	if forbidden {
		bot.logger.Printf("Trying to fetch '%s' resulted in a 403 error", user.Name)
	} else if gone {
		bot.logger.Printf("User '%s' not found", user.Name)
	}

	var about UserQuery
	if forbidden {
		about = bot.scanner.AboutUser(user.Name)
		if about.Error != nil {
			return false, about.Error
		}
	}

	if gone || about.User.Suspended {
		err := bot.storage.SetSuspended(user.Name, true)
		if err != nil {
			return false, err
		}

		if bot.Suspended != nil {
			bot.logger.Printf("User '%s' has been suspended or shadowbanned", user.Name)
			bot.Suspended <- user
		}

		return true, nil
	}

	return false, nil
}

func (bot *Bot) saveCommentsPage(user User) (string, int, error) {
	comments, position, status, err := bot.scanner.UserComments(user.Name, user.Position)
	if err != nil {
		return "", status, err
	}
	if status == 403 || status == 404 {
		return position, status, nil
	}

	err = bot.storage.SaveCommentsPage(comments, user)
	if err != nil {
		return "", status, err
	}

	err = bot.AlertIfHighScore(comments)
	if err != nil {
		return "", status, err
	}

	if len(comments) > 0 && bot.maxAgeReached(comments) && !user.New {
		return "", status, nil
	}
	return position, status, nil
}

func (bot *Bot) maxAgeReached(comments []Comment) bool {
	last_comment := comments[len(comments)-1]

	oldest := last_comment.Created
	// Use time.Time.Round to remove the monotonic clock measurement, as
	// we don't need it for the precision we want and one parameter depends
	// on an external source (the comments' timestamps).
	now := time.Now().Round(0)
	return now.Sub(oldest) > bot.Conf.MaxAge.Value
}

func (bot *Bot) AlertIfHighScore(comments []Comment) error {
	if bot.highScoresFeed == nil {
		return nil
	}

	for _, comment := range comments {

		if comment.Score < bot.highScore {

			is_known, err := bot.storage.IsKnownObject(comment.Id)
			if err != nil {
				return err
			}

			if is_known {
				continue
			}

			bot.logger.Printf("New high-scoring comment found: %+v", comment)
			err = bot.storage.SaveKnownObject(comment.Id)
			if err != nil {
				return err
			}

			bot.highScoresFeed <- comment

		}

	}

	return nil
}

func StringInSlice(str string, slice []string) bool {
	for _, elem := range slice {
		if str == elem {
			return true
		}
	}
	return false
}
