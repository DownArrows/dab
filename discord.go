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
	AddUser       chan UserQuery
}

func NewDiscordBot(
	storage *Storage,
	log_out io.Writer,
	token string,
	general string,
	log_chan string,
	admin string,
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
		AddUser:       make(chan UserQuery),
	}

	session.AddHandler(func(s *discordgo.Session, msg *discordgo.MessageCreate) {
		dbot.OnMessage(msg)
	})

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		dbot.OnReady(admin, general, log_chan)
	})

	return dbot, nil
}

func (bot *DiscordBot) Run() {
	err := bot.client.Open()
	if err != nil {
		panic(err)
	}

	err = bot.client.UpdateStatus(0, "Downvote Counter")
	if err != nil {
		panic(err)
	}
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
	} else if strings.HasPrefix(content, "!exists ") {
		log.Print(author + " wants to check if a user is registered")
		err = bot.UserExists(content, channel, msg)
	}

	if err != nil {
		bot.logger.Print(err)
	}
}

func (bot *DiscordBot) UserExists(content, channel string, msg *discordgo.MessageCreate) error {
	username := strings.TrimPrefix(content, "!exists ")

	users, err := bot.storage.ListUsers()
	if err != nil {
		return err
	}

	status := "not found"
	for _, user := range users {
		if user.Username(username) {
			username = user.Name
			status = fmt.Sprintf("found")
			break
		}
	}

	response := fmt.Sprintf("<@%s> User %s %s.", msg.Author.ID, username, status)
	_, err = bot.client.ChannelMessageSend(channel, response)
	return err
}

func (bot *DiscordBot) Register(msg *discordgo.MessageCreate) error {
	names := strings.Split(strings.TrimPrefix(msg.Content, "!register "), " ")
	bot.logger.Print(msg.Author.Username, " wants to register ", names)

	statuses := make([]string, 0, len(names))
	for _, name := range names {
		err := bot.client.ChannelTyping(msg.ChannelID)
		if err != nil {
			return err
		}

		bot.AddUser <- UserQuery{User: User{Name: name, Hidden: false}}
		reply := <-bot.AddUser

		var status string
		if reply.Error != nil {
			status = fmt.Sprintf("%s: %s", reply.User.Name, reply.Error)
		} else if !reply.Exists {
			status = fmt.Sprintf("%s: not found", reply.User.Name)
		} else {
			status = fmt.Sprintf("%s: ok", reply.User.Name)
		}
		statuses = append(statuses, status)
	}

	status := strings.Join(statuses, ", ")
	if len(status) > 1900 {
		status = "registrations done, check the logs for more details."
	}
	response := fmt.Sprintf("<@%s> registration: %s", msg.Author.ID, status)
	_, err := bot.client.ChannelMessageSend(msg.ChannelID, response)
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
	err := bot.client.ChannelTyping(bot.General.ID)
	if err != nil {
		return err
	}

	fortune := bot.getFortune()
	_, err = bot.client.ChannelMessageSend(bot.General.ID, fortune)
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
	err := bot.client.ChannelTyping(channelID)
	if err != nil {
		return err
	}

	res := bot.storage.GetUser(username)
	if res.Error != nil {
		return res.Error
	}

	if !res.Exists {
		reply := fmt.Sprintf("<@%s> user %s not found.", author.ID, username)
		_, err = bot.client.ChannelMessageSend(channelID, reply)
		return err
	}

	total, err := bot.storage.GetTotalKarma(username)
	if err != nil {
		return err
	}

	negative, err := bot.storage.GetNegativeKarma(username)
	if err != nil {
		return err
	}

	reply := fmt.Sprintf("<@%s> karma for %s: %d / %d", author.ID, res.User.Name, total, negative)
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
