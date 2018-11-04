package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

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

func (bot *RedditBot) CheckUnsuspended(delay time.Duration) chan User {
	ch := make(chan User)

	go func() {

		for {

			time.Sleep(delay)

			for _, user := range bot.storage.ListSuspended() {
				res := bot.scanner.AboutUser(user.Name)
				if res.Error != nil {
					bot.logger.Printf("unsuspension checker, while checking \"%s\": %s", user.Name, res.Error)
					continue
				}

				if res.Exists && !res.User.Suspended {
					if err := bot.storage.UnSuspendUser(user.Name); err != nil {
						bot.logger.Printf("unsuspension checker, while checking \"%s\": %s", user.Name, res.Error)
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

func (bot *RedditBot) Run() {
	// Band-aid until proper closing sequence is written.
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

func (bot *RedditBot) scanOnce(full_scan bool) error {
	users, err := bot.getUsersOrWait(full_scan)
	if err != nil {
		return err
	}
	return bot.scanUsers(users)
}

func (bot *RedditBot) getUsersOrWait(full_scan bool) ([]User, error) {
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
	return users, nil
}

func (bot *RedditBot) scanUsers(users []User) error {
	for _, user := range users {

		if err := bot.allRelevantComments(user); err != nil {
			// That error is probably network-related, so just log it
			// and wait for the network or reddit to work again.
			bot.logger.Print("error when fetching and saving comments: ", err)
		}
	}
	return nil
}

func (bot *RedditBot) allRelevantComments(user User) error {
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
			if err := bot.storage.NotNewUser(user.Name); err != nil {
				return err
			}
		}

		if user.Position == "" {
			if err := bot.storage.ResetPosition(user.Name); err != nil {
				return err
			}
			break
		}

	}

	return nil
}

func (bot *RedditBot) ifSuspended(user User, status int) (bool, error) {
	gone := status == 404
	forbidden := status == 403

	if forbidden {
		bot.logger.Printf("trying to fetch '%s' resulted in a 403 error", user.Name)
	} else if gone {
		bot.logger.Printf("user '%s' not found", user.Name)
	}

	var about UserQuery
	if forbidden {
		about = bot.scanner.AboutUser(user.Name)
		if about.Error != nil {
			return false, about.Error
		}
	}

	if gone || about.User.Suspended {
		if err := bot.storage.SuspendUser(user.Name); err != nil {
			return false, err
		}

		if bot.Suspended != nil {
			bot.logger.Printf("user '%s' has been suspended or shadowbanned", user.Name)
			bot.Suspended <- user
		}

		return true, nil
	}

	return false, nil
}

func (bot *RedditBot) saveCommentsPage(user User) (string, int, error) {
	comments, position, status, err := bot.scanner.UserComments(user.Name, user.Position)
	if err != nil {
		return "", status, err
	}
	if status == 403 || status == 404 {
		return position, status, nil
	}

	if err := bot.storage.SaveCommentsPage(comments, user); err != nil {
		return "", status, err
	}

	if err := bot.AlertIfHighScore(comments); err != nil {
		return "", status, err
	}

	if len(comments) > 0 && bot.maxAgeReached(comments) && !user.New {
		return "", status, nil
	}
	return position, status, nil
}

func (bot *RedditBot) maxAgeReached(comments []Comment) bool {
	last_comment := comments[len(comments)-1]

	oldest := last_comment.CreatedTime()
	// Use time.Time.Round to remove the monotonic clock measurement, as
	// we don't need it for the precision we want and one parameter depends
	// on an external source (the comments' timestamps).
	now := time.Now().Round(0)
	return now.Sub(oldest) > bot.Conf.MaxAge.Value
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
