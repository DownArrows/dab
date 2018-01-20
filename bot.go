package main

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

type UserAddition struct {
	Name   string
	Hidden bool
	Exists bool
	Error  error
}

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
	for i := 0; true; i++ {
		err := bot.ScanOnce()
		if err != nil {
			return err
		}

		if i %= 10; i == 0 {
			err = bot.storage.Vacuum()
			if err != nil {
				bot.logger.Print("Database vacuum error: ", err)
			}
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

	return bot.ScanUsers(users)
}

func (bot *Bot) getUsersOrWait() ([]User, error) {
	var users []User
	var err error
	for {
		users, err = bot.storage.ListUsers()
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
			// That error is probably network-related, so just log it
			// and wait for the network or reddit to work again.
			bot.logger.Print("Error when fetching and saving comments: ", err)
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

func (bot *Bot) SaveCommentsPage(user User) (string, error) {
	comments, position, err := bot.scanner.UserComments(user.Name, user.Position)
	if err != nil {
		return "", err
	}

	err = bot.storage.SaveCommentsPage(comments, user)
	if err != nil {
		return "", err
	}

	if len(comments) > 0 && bot.maxAgeReached(comments) && !user.New {
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

func (bot *Bot) AddUserListen(requests chan chan UserAddition) {
	for query_chan := range requests {
		bot.logger.Print("Init addition of new users.")
		go bot.addUserServer(query_chan)
	}
}

func (bot *Bot) addUserServer(queries chan UserAddition) {
	for query := range queries {
		bot.logger.Print("Received query to add a new user: ", query)

		exists, err := bot.AddUser(query.Name, query.Hidden)
		if err != nil {
			bot.logger.Print("Error when adding the new user ", query.Name, err)
		}

		reply := UserAddition{
			Name:   query.Name,
			Hidden: query.Hidden,
			Exists: exists,
			Error:  err,
		}

		queries <- reply
	}
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

	exists, case_name, created, suspended, err := bot.scanner.AboutUser(username)
	if err != nil {
		return false, err
	}

	if suspended {
		bot.logger.Print("User ", username, " was suspended, adding anyway")
	}

	if !exists {
		bot.logger.Print("User ", username, " not found")
		return false, err
	}

	err = bot.storage.AddUser(case_name, hidden, created)
	if err != nil {
		return false, err
	}

	bot.logger.Print("New user ", case_name, " successfully added")
	return true, nil
}

func (bot *Bot) HasUser(username string) (bool, error) {
	users, err := bot.storage.ListUsers()
	if err != nil {
		return false, err
	}

	for _, user := range users {
		if strings.ToLower(user.Name) == username {
			return true, nil
		}
	}
	return false, nil
}

func (bot *Bot) StreamSub(sub string, ch chan Comment) {
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

		time.Sleep(time.Duration(1) * time.Second)
	}
}

func StringInSlice(str string, slice []string) bool {
	for _, elem := range slice {
		if str == elem {
			return true
		}
	}
	return false
}
