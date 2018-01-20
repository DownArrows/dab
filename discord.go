package main

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"io"
	"log"
	"math/rand"
	"regexp"
	"strings"
)

type DiscordBot struct {
	logger        *log.Logger
	storage       *Storage
	client        *discordgo.Session
	LinkReactions []string
	prevFortune   string
	redditLink    *regexp.Regexp
	General       *discordgo.Channel
	LogChan       *discordgo.Channel
	Admin         *discordgo.User
	Fortunes      []string
	AddUser       chan chan UserAddition
}

func NewDiscordBot(
	storage *Storage,
	log_out io.Writer,
	token string,
	general string,
	log_chan string,
	admin string,
	addUser chan chan UserAddition,
) (*DiscordBot, error) {
	logger := log.New(log_out, "discordbot: ", log.LstdFlags)

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	fortunes, err := storage.GetFortunes()
	if err != nil {
		return nil, err
	}

	dbot := &DiscordBot{
		client:        session,
		logger:        logger,
		storage:       storage,
		LinkReactions: []string{"👌", "💗", "🔥", "💯"},
		redditLink:    regexp.MustCompile(`(?s:.*reddit\.com/r/\w+/comments/.*)`),
		Fortunes:      fortunes,
		AddUser:       addUser,
	}

	session.AddHandler(func(s *discordgo.Session, msg *discordgo.MessageCreate) {
		dbot.OnMessage(msg)
	})

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		dbot.OnReady(admin, general, log_chan)
	})

	return dbot, nil
}

func (bot *DiscordBot) Run(kill chan bool) {
	err := bot.client.Open()
	if err != nil {
		bot.logger.Fatal(err)
	}

	<-kill
	bot.client.Close()
}

func (bot *DiscordBot) RedditEvents(evts chan Comment) {
	var err error
	for comment := range evts {
		bot.logger.Print("New event from reddit: ", comment)

		if comment.Author == "DownvoteTrollingBot" || comment.Author == "DownvoteTrollingBot2" {
			msg := "@everyone https://www.reddit.com" + comment.Permalink
			_, err = bot.client.ChannelMessageSend(bot.General.ID, msg)
		}

		if err != nil {
			bot.logger.Print("Reddit events listener: ", err)
		}
	}
}

func (bot *DiscordBot) OnReady(admin, general, log_chan string) {
	var err error
	bot.General, err = bot.client.Channel(general)
	if err != nil {
		bot.logger.Fatal(err)
	}

	bot.LogChan, err = bot.client.Channel(log_chan)
	if err != nil {
		bot.logger.Fatal(err)
	}

	bot.Admin, err = bot.client.User(admin)
	if err != nil {
		bot.logger.Fatal(err)
	}
	bot.logger.Print("Initialization ok")
}

func (bot *DiscordBot) OnMessage(msg *discordgo.MessageCreate) {
	var err error
	if msg.Author.ID == bot.client.State.User.ID {
		return
	}

	content := msg.Content
	channel := msg.ChannelID
	author := FullAuthorName(msg)
	private, err := bot.IsDMChannel(channel)
	if err != nil {
		bot.logger.Print(err)
		return
	}

	if channel == bot.General.ID && bot.redditLink.MatchString(content) && !strings.Contains(content, "!nolog") {
		bot.logger.Print("Link to a comment on reddit posted by ", author)
		err = bot.RedditCommentLink(msg)
	} else if private && content == "!fortune" {
		bot.logger.Print(author, " has asked for a fortune")
		err = bot.Fortune()
	} else if private && msg.Author.ID == bot.Admin.ID && strings.HasPrefix(content, "!addfortune ") {
		bot.logger.Print(author, " wants to add a fortune")
		fortune := strings.TrimPrefix(content, "!addfortune ")
		err = bot.AddFortune(fortune)
		if err == nil {
			reply := msg.Author.Mention() + " fortune saved."
			_, err = bot.client.ChannelMessageSend(msg.ChannelID, reply)
		}
	} else if strings.HasPrefix(content, "!karma ") {
		err = bot.Karma(channel, msg.Author, strings.TrimPrefix(content, "!karma "))
	} else if content == "!ping" && msg.Author.ID == bot.Admin.ID {
		_, err = bot.client.ChannelMessageSend(msg.ChannelID, "pong")
	} else if strings.HasPrefix(content, "!register ") {
		err = bot.Register(msg)
	} else if content == "!registered" {
		log.Print(author + " asked for the list of registered users")
		err = bot.ListRegistered(msg)
	}

	if err != nil {
		bot.logger.Print(err)
	}
}

func (bot *DiscordBot) ListRegistered(msg *discordgo.MessageCreate) error {
	users, err := bot.storage.ListUsers()
	if err != nil {
		return err
	}

	usernames := make([]string, 0, len(users))
	for _, user := range users {
		usernames = append(usernames, user.Name)
	}

	list := strings.Join(usernames, ", ")
	response := fmt.Sprintf("<@%s> registered users: %s", msg.Author.ID, list)
	_, err = bot.client.ChannelMessageSend(msg.ChannelID, response)
	return err
}

func (bot *DiscordBot) Register(msg *discordgo.MessageCreate) error {
	names := strings.Split(strings.TrimPrefix(msg.Content, "!register "), " ")
	bot.logger.Print(msg.Author.Username, " wants to register ", names)

	err := bot.client.UpdateStatus(1, "")
	if err != nil {
		return err
	}

	statuses := make([]string, 0, len(names))
	queries := make(chan UserAddition)
	bot.AddUser <- queries

	for _, name := range names {
		queries <- UserAddition{Name: name, Hidden: false}
		reply := <-queries

		var status string
		if reply.Error != nil {
			status = fmt.Sprintf("%s: %s", reply.Name, reply.Error)
		} else if !reply.Exists {
			status = fmt.Sprintf("%s: not found", reply.Name)
		} else {
			status = fmt.Sprintf("%s: ok", reply.Name)
		}
		statuses = append(statuses, status)
	}

	close(queries)

	err = bot.client.UpdateStatus(0, "")
	if err != nil {
		return err
	}

	status := strings.Join(statuses, ", ")
	response := fmt.Sprintf("<@%s> registration: %s", msg.Author.ID, status)
	_, err = bot.client.ChannelMessageSend(msg.ChannelID, response)
	return err
}

func (bot *DiscordBot) RedditCommentLink(msg *discordgo.MessageCreate) error {
	response := FullAuthorName(msg) + ": " + msg.Content

	i := rand.Int31n(int32(len(bot.LinkReactions)))
	reaction := bot.LinkReactions[i]
	err := bot.client.MessageReactionAdd(msg.ChannelID, msg.ID, reaction)
	if err != nil {
		return err
	}

	_, err = bot.client.ChannelMessageSend(bot.LogChan.ID, response)
	return err
}

func (bot *DiscordBot) Fortune() error {
	fortune := bot.getFortune()
	_, err := bot.client.ChannelMessageSend(bot.General.ID, fortune)
	return err
}

func (bot *DiscordBot) AddFortune(fortune string) error {
	err := bot.storage.SaveFortune(fortune)
	if err != nil {
		return err
	}
	bot.Fortunes = append(bot.Fortunes, fortune)
	return nil
}

func (bot *DiscordBot) getFortune() string {
	i := rand.Int31n(int32(len(bot.Fortunes)))
	fortune := bot.Fortunes[i]
	if fortune == bot.prevFortune {
		return bot.getFortune()
	}
	bot.prevFortune = fortune
	return fortune
}

func (bot *DiscordBot) Karma(channelID string, author *discordgo.User, username string) error {
	total, err := bot.storage.GetTotalKarma(username)
	if err != nil {
		return err
	}

	negative, err := bot.storage.GetNegativeKarma(username)
	if err != nil {
		return err
	}

	reply := fmt.Sprintf("<@%s> karma for %s: %d / %d", author.ID, username, total, negative)
	_, err = bot.client.ChannelMessageSend(channelID, reply)
	return err
}

func (bot *DiscordBot) IsDMChannel(channelID string) (bool, error) {
	channel, err := bot.client.Channel(channelID)
	if err != nil {
		return false, err
	}
	return channel.Type == discordgo.ChannelTypeDM, nil
}

func FullAuthorName(msg *discordgo.MessageCreate) string {
	return msg.Author.Username + "#" + msg.Author.Discriminator
}
