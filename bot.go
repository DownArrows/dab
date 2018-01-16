package main

import (
	"fmt"
	"io"
	"log"
	"time"
)

type Bot struct {
	MaxAge     time.Duration
	MaxQueries int
	scanner    RedditScanner
	storage    *Storage
	logger     *log.Logger
}

func NewBot(
	scanner RedditScanner,
	storage *Storage,
	log_out io.Writer,
	max_age_hours int64,
	max_queries int,
) *Bot {
	logger := log.New(log_out, "bot: ", log.LstdFlags)
	bot := &Bot{
		MaxAge:     time.Duration(max_age_hours) * time.Hour,
		MaxQueries: max_queries,
		scanner:    scanner,
		storage:    storage,
		logger:     logger,
	}

	return bot
}

func (bot *Bot) Run() error {
	for {
		err := bot.ScanOnce()
		if err != nil {
			return err
		}
	}
	return nil
}

func (bot *Bot) ScanOnce() error {
	bot.logger.Print("Scanning all known users")
	users, err := bot.getUsersOrWait()
	if err != nil {
		return err
	}

	err = bot.ScanUsers(users)
	if err != nil {
		// That error is probably network-related, so just log it
		// and wait for the network or reddit to work again.
		bot.logger.Print("Error when fetching and saving comments: ", err)
	}
	return nil
}

func (bot *Bot) getUsersOrWait() ([]User, error) {
	var users []User
	var err error
	for {
		bot.storage.Lock()
		users, err = bot.storage.ListUsers()
		bot.storage.Unlock()
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

func (bot *Bot) ScanUsers(users []User) error {
	for _, user := range users {
		err := bot.AllRelevantComments(user)
		if err != nil {
			return err
		}
	}
	return nil
}

func (bot *Bot) AllRelevantComments(user User) error {
	var err error

	for i := 0; i < bot.MaxQueries; i++ {

		template := "Fetching batch n°%d of comments from %s, position \"%s\""
		bot.logger.Print(fmt.Sprintf(template, i+1, user.Name, user.Position))

		user.Position, err = bot.SaveCommentsPage(user)
		if err != nil {
			return err
		}

		if user.Position == "" && user.New {
			bot.storage.Lock()
			err = bot.storage.NotNewUser(user.Name)
			bot.storage.Unlock()
			if err != nil {
				return err
			}
		}

		if user.Position == "" {
			bot.storage.Lock()
			err = bot.storage.ResetPosition(user.Name)
			bot.storage.Unlock()
			if err != nil {
				return err
			}
			break
		}

	}

	return nil
}

func (bot *Bot) SaveCommentsPage(user User) (string, error) {
	comments, position, err := bot.scanner.UserComments(user.Name, user.Position)
	if err != nil {
		return "", err
	}

	bot.storage.Lock()
	err = bot.storage.SaveCommentsPage(comments, user)
	bot.storage.Unlock()
	if err != nil {
		return "", err
	}

	if bot.maxAgeReached(comments) && !user.New {
		return "", nil
	}
	return position, nil
}

func (bot *Bot) maxAgeReached(comments []Comment) bool {
	last_comment := comments[len(comments)-1]

	oldest := time.Unix(int64(last_comment.Created), 0)
	// Use time.Time.Round to remove the monotonic clock measurement, as
	// we don't need it for the precision we want and one parameter depends
	// on an external source (the comments' timestamps).
	now := time.Now().Round(0)
	return now.Sub(oldest) > bot.MaxAge
}

func (bot *Bot) AddUser(username string, hidden bool) (bool, error) {
	bot.logger.Print("Trying to add user ", username)

	has_user, err := bot.HasUser(username)
	if err != nil {
		return false, err
	}
	if has_user {
		bot.logger.Print(username, " already exists")
		return true, nil
	}

	exists, err := bot.scanner.UserExists(username)
	if err != nil || !exists {
		return false, err
	}

	bot.storage.Lock()
	err = bot.storage.AddUser(username, hidden)
	bot.storage.Unlock()
	if err != nil {
		return false, err
	}

	bot.logger.Print("New user ", username, " successfully added")
	return true, nil
}

func (bot *Bot) HasUser(username string) (bool, error) {
	bot.storage.Lock()
	users, err := bot.storage.ListUsers()
	bot.storage.Unlock()
	if err != nil {
		return false, err
	}

	for _, user := range users {
		if user.Name == username {
			return true, nil
		}
	}
	return false, nil
}
