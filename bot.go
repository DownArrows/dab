package main

import (
	"io"
	"log"
	"time"
)

type UserAddStatus struct {
	User string
	Ok   bool
}

type Bot struct {
	Users        []string
	NewUsers     chan (chan []UserAddStatus)
	prevComments map[string][]Comment
	MaxAge       time.Duration
	client       *RedditClient
	storage      *Storage
	logger       *log.Logger
}

func NewBot(auth RedditAuth, storage *Storage, log_out io.Writer, max_age_hours int64) (*Bot, error) {
	logger := log.New(log_out, "bot: ", log.LstdFlags)

	reddit, err := NewRedditClient(auth)
	if err != nil {
		return nil, err
	}

	storage.Lock()
	users, err := storage.ListUsers()
	storage.Unlock()
	if err != nil {
		return nil, err
	}

	bot := &Bot{
		Users:        users,
		NewUsers:     make(chan (chan []UserAddStatus)),
		prevComments: make(map[string][]Comment),
		MaxAge:       time.Duration(max_age_hours) * time.Hour,
		client:       reddit,
		storage:      storage,
		logger:       logger,
	}

	return bot, nil
}

func (bot *Bot) Scan(end chan bool) {
	done := make(chan bool)

	for {
		if len(bot.Users) == 0 {
			query_ch := <-bot.NewUsers
			bot.newUserQuery(query_ch)
		}

		for _, user := range bot.Users {

			go func() {
				err := bot.GetAndSaveComments(user)
				if err != nil {
					bot.logger.Print("Error when fetching and saving comments of user ", user, err)
				}
				done <- true
			}()

			select {
			case <-done:
			case query_ch := <-bot.NewUsers:
				bot.newUserQuery(query_ch)
			}
		}
	}
	end <- true
}

func (bot *Bot) newUserQuery(query_ch chan []UserAddStatus) {
	bot.logger.Print("Init addition of new users.")
	query := <-query_ch

	bot.logger.Print("Received query to add new users: ", query)
	resp := make([]UserAddStatus, len(query))

	for i, status := range query {
		new_user := status.User
		ok, err := bot.addUser(new_user, status.Ok)
		if err != nil {
			bot.logger.Print("Error when adding the new user ", new_user, err)
			resp[i] = UserAddStatus{User: new_user, Ok: false}
		}
		resp[i] = UserAddStatus{User: new_user, Ok: ok}
	}

	query_ch <- resp
}

func (bot *Bot) addUser(username string, hidden bool) (bool, error) {
	bot.logger.Print("Trying to add user ", username)
	if bot.hasUser(username) {
		bot.logger.Print(username, " already exists")
		return true, nil
	}
	_, err := bot.client.RawRequest("GET", "/u/"+username, nil)
	if err != nil {
		return false, err
	}

	bot.Users = append(bot.Users, username)
	bot.storage.Lock()
	err = bot.storage.AddUser(username, hidden)
	bot.storage.Unlock()
	if err != nil {
		return false, err
	}
	bot.logger.Print("New user ", username, " successfully added")
	return true, nil
}

func (bot *Bot) hasUser(username string) bool {
	for _, user := range bot.Users {
		if username == user {
			return true
		}
	}
	return false
}

func (bot *Bot) GetAndSaveComments(user string) error {
	bot.logger.Print("Fetching comments from ", user)

	var comments []Comment
	var err error = nil
	var oldest time.Time
	after := ""
	overlaps := false
	first := true

	// Use time.Time.Round to remove the monotonic clock measurement, as
	// we don't need it for the precision we want and one parameter depends
	// on an external source (the comments' timestamps).
	for now := time.Now().Round(0); !overlaps || now.Sub(oldest) < bot.MaxAge; {
		if after != "" {
			bot.logger.Print("Fetching another batch of comments from ", user, " after ", after)
		}

		comments, after, err = bot.client.FetchComments(user, after)
		if err != nil {
			return err
		}
		oldest = time.Unix(int64(comments[len(comments)-1].Created), 0)

		bot.storage.Lock()
		err = bot.storage.SaveComment(comments...)
		bot.storage.Unlock()
		if err != nil {
			return err
		}

		overlaps = listingOverlap(bot.prevComments[user], comments)
		if first {
			bot.prevComments[user] = comments
			first = false
		} else if after == "" {
			break
		}
	}
	return nil
}

func listingOverlap(prev, current []Comment) bool {
	last := current[len(current)-1]
	for _, comment := range prev {
		if comment.Id == last.Id {
			return true
		}
	}
	return false
}
