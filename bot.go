package main

import (
	"errors"
	"io"
	"log"
	"time"
)

type Bot struct {
	MaxAge     time.Duration
	MaxQueries int
	Suspended  chan User
	scanner    RedditScanner
	storage    *Storage
	logger     *log.Logger
}

func NewBot(
	scanner RedditScanner,
	storage *Storage,
	logOut io.Writer,
	maxAge time.Duration,
	maxBatches int,
) *Bot {
	logger := log.New(logOut, "bot: ", log.LstdFlags)

	bot := &Bot{
		MaxAge:     maxAge,
		MaxQueries: maxBatches,
		scanner:    scanner,
		storage:    storage,
		logger:     logger,
	}

	return bot
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

func (bot *Bot) AddUser(username string, hidden bool, force_suspended bool) UserQuery {
	bot.logger.Print("Trying to add user ", username)
	query := UserQuery{User: User{Name: username}}

	query = bot.storage.GetUser(username)
	if query.Error != nil {
		return query
	} else if query.Exists {
		msg := username + " already exists"
		bot.logger.Print(msg)
		query.Error = errors.New(msg)
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
			err := errors.New("User " + query.User.Name + " can't be added, forced mode not enabled")
			query.Error = err
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
						bot.logger.Printf("Unsuspension checker, while checking \"%s\": %s", user.Name, res)
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
	for {
		err := bot.scanOnce()
		if err != nil {
			panic(err)
		}
	}
}

func (bot *Bot) scanOnce() error {
	bot.logger.Print("Scanning all known users")
	users, err := bot.getUsersOrWait()
	if err != nil {
		return err
	}

	return bot.scanUsers(users)
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

	for i := 0; i < bot.MaxQueries; i++ {
		template := "Fetching batch n°%d of comments from %s, position \"%s\""
		bot.logger.Printf(template, i+1, user.Name, user.Position)

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
		bot.logger.Print("trying to fetch " + user.Name + " resulted in a 403 error")
	} else if gone {
		bot.logger.Print(user.Name + " not found")
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
			bot.logger.Print("User " + user.Name + " has been suspended or shadowbanned")
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
	return now.Sub(oldest) > bot.MaxAge
}

func StringInSlice(str string, slice []string) bool {
	for _, elem := range slice {
		if str == elem {
			return true
		}
	}
	return false
}
