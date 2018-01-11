package main

import (
	"io"
	"log"
	"errors"
)

type UserAddStatus struct {
	User string
	Ok bool
}

type Bot struct {
	Users []string
	NewUsers chan (chan []UserAddStatus)
	client *RedditClient
	storage *Storage
	logger *log.Logger
}

func NewBot(auth RedditAuth, storage *Storage, log_out io.Writer,) (*Bot, error) {
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
		Users: users,
		NewUsers: make(chan (chan []UserAddStatus)),
		client: reddit,
		storage: storage,
		logger: logger,
	}

	return bot, nil
}

func (bot *Bot) Scan(end chan bool) {
	done := make(chan bool)

	for {
		for _, user := range bot.Users {
			go func() {
				err := bot.GetAndSaveComments(user)
				if err != nil {
					bot.logger.Print("Error when fetching and saving comments of user ", user, err)
				}
				done<- true
			}()
			select {
			case <-done:
			case query_ch := <-bot.NewUsers:
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
				query_ch<- resp
			}
		}
	}
	end<- true
}

func (bot *Bot) addUser(username string, hidden bool) (bool, error) {
	bot.logger.Print("Trying to add user ", username)
	if bot.hasUser(username) {
		bot.logger.Print(username, " already exists")
		return true, nil
	}
	err := bot.GetAndSaveComments(username)
	if err != nil {
		if err == errors.New("bad status code: 404") {
			bot.logger.Print("New user ", username, " not found")
			return false, nil
		}
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
	bot.logger.Print(username, " not found")
	return false
}


func (bot *Bot) GetAndSaveComments(user string) error {
	bot.logger.Print("Fetching comments of ", user)
	comments, err := bot.client.FetchComments(user, "")
	if err != nil {
		return err
	}
	bot.storage.Lock()
	err = bot.storage.SaveComment(comments...)
	bot.storage.Unlock()
	return err
}
