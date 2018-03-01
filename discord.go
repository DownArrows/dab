package main

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"io"
	"log"
	"math/rand"
	"regexp"
	"strings"
	"time"
)

type DiscordBot struct {
	logger        *log.Logger
	storage       *Storage
	client        *discordgo.Session
	LinkReactions []string
	redditLink    *regexp.Regexp
	General       *discordgo.Channel
	LogChan       *discordgo.Channel
	Admin         *discordgo.User
	AddUser       chan UserQuery
}

func NewDiscordBot(
	storage *Storage,
	logOut io.Writer,
	token string,
	general string,
	logChan string,
	admin string,
) (*DiscordBot, error) {
	logger := log.New(logOut, "discordbot: ", log.LstdFlags)

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	dbot := &DiscordBot{
		client:        session,
		logger:        logger,
		storage:       storage,
		LinkReactions: []string{"👌", "💗", "🔥", "💯"},
		redditLink:    regexp.MustCompile(`(?s:.*reddit\.com/r/\w+/comments/.*)`),
		AddUser:       make(chan UserQuery),
	}

	session.AddHandler(func(s *discordgo.Session, msg *discordgo.MessageCreate) {
		dbot.onMessage(msg)
	})

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		dbot.onReady(admin, general, logChan)
	})

	return dbot, nil
}

func (bot *DiscordBot) Run() {
	err := bot.client.Open()
	if err != nil {
		panic(err)
	}

	go bot.setPlayingStatus()
}

func (bot *DiscordBot) setPlayingStatus() {
	for {
		err := bot.client.UpdateStatus(0, "Downvote Counter")
		if err != nil {
			bot.logger.Print("Couldn't set status on discord")
		}
		time.Sleep(time.Hour)
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

func (bot *DiscordBot) SignalSuspensions(suspensions chan User) {
	for user := range suspensions {
		msg := fmt.Sprintf("RIP %s 🙏", user.Name)
		_, err := bot.client.ChannelMessageSend(bot.General.ID, msg)
		if err != nil {
			bot.logger.Print("Suspensions listener: ", err)
		}
	}
}

func (bot *DiscordBot) SignalUnsuspensions(ch chan User) {
	for user := range ch {
		msg := fmt.Sprintf("🌈 %s has been unsuspended! 🌈", user.Name)
		_, err := bot.client.ChannelMessageSend(bot.General.ID, msg)
		if err != nil {
			bot.logger.Print("Unsuspensions listener: ", err)
		}
	}
}

func (bot *DiscordBot) onReady(admin, general, logChan string) {
	var err error
	bot.General, err = bot.client.Channel(general)
	if err != nil {
		bot.logger.Fatal(err)
	}

	bot.LogChan, err = bot.client.Channel(logChan)
	if err != nil {
		bot.logger.Fatal(err)
	}

	bot.Admin, err = bot.client.User(admin)
	if err != nil {
		bot.logger.Fatal(err)
	}
	bot.logger.Print("Initialization ok")
}

func (bot *DiscordBot) onMessage(msg *discordgo.MessageCreate) {
	var err error
	if msg.Author.ID == bot.client.State.User.ID {
		return
	}

	content := msg.Content
	channel := msg.ChannelID
	author := fullAuthorName(msg)
	if err != nil {
		bot.logger.Print(err)
		return
	}

	if bot.isLoggableRedditLink(channel, content) {
		bot.logger.Print("Link to a comment on reddit posted by ", author)
		err = bot.processRedditLink(msg)
	} else if strings.HasPrefix(content, "!karma ") {
		err = bot.karma(msg, strings.TrimPrefix(content, "!karma "))
	} else if content == "!ping" && msg.Author.ID == bot.Admin.ID {
		_, err = bot.client.ChannelMessageSend(msg.ChannelID, "pong")
	} else if strings.HasPrefix(content, "!register ") {
		err = bot.register(msg)
	} else if strings.HasPrefix(content, "!unregister ") && msg.Author.ID == bot.Admin.ID {
		err = bot.unregister(msg)
	} else if strings.HasPrefix(content, "!exists ") {
		log.Print(author + " wants to check if a user is registered")
		err = bot.userExists(content, channel, msg)
	} else if content == "!sip" || content == "!sipthebep" {
		response := `More like N0000 1 cares 🔥 This shitpost is horrible 👎👎👎`
		_, err = bot.client.ChannelMessageSend(msg.ChannelID, response)
	} else if content == "!sep" || content == "!separator" || content == "!=" {
		err = bot.separator(msg)
	}

	if err != nil {
		bot.logger.Print(err)
	}
}

func (bot *DiscordBot) isLoggableRedditLink(channel, content string) bool {
	return (channel == bot.General.ID &&
		bot.redditLink.MatchString(content) &&
		!strings.Contains(strings.ToLower(content), "!nolog"))
}

func (bot *DiscordBot) processRedditLink(msg *discordgo.MessageCreate) error {
	err := bot.addRandomReactionTo(msg)
	if err != nil {
		return err
	}
	return bot.postInLogChannel(fullAuthorName(msg) + ": " + msg.Content)
}

func (bot *DiscordBot) addRandomReactionTo(msg *discordgo.MessageCreate) error {
	nb_reactions := len(bot.LinkReactions)
	rand_index := rand.Int31n(int32(nb_reactions))
	reaction := bot.LinkReactions[rand_index]
	return bot.client.MessageReactionAdd(msg.ChannelID, msg.ID, reaction)
}

func (bot *DiscordBot) postInLogChannel(response string) error {
	_, err := bot.client.ChannelMessageSend(bot.LogChan.ID, response)
	return err
}

func (bot *DiscordBot) register(msg *discordgo.MessageCreate) error {
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

func (bot *DiscordBot) unregister(msg *discordgo.MessageCreate) error {
	names := strings.Split(strings.TrimPrefix(msg.Content, "!unregister "), " ")
	bot.logger.Print(msg.Author.Username, " wants to unregister ", names)

	results := make([]string, len(names))
	for i, name := range names {
		err := bot.storage.DelUser(name)
		if err != nil {
			results[i] = fmt.Sprintf("%s: error %s", name, err)
		} else {
			results[i] = fmt.Sprintf("%s: ok", name)
		}
	}

	result := strings.Join(results, ", ")
	response := fmt.Sprintf("<@%s> unregister: %s", msg.Author.ID, result)
	_, err := bot.client.ChannelMessageSend(msg.ChannelID, response)
	return err
}

func (bot *DiscordBot) userExists(content, channel string, msg *discordgo.MessageCreate) error {
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

func (bot *DiscordBot) karma(msg *discordgo.MessageCreate, username string) error {
	err := bot.client.ChannelTyping(msg.ChannelID)
	if err != nil {
		return err
	}

	res := bot.storage.GetUser(username)
	if res.Error != nil {
		return res.Error
	}

	if !res.Exists {
		reply := fmt.Sprintf("<@%s> user %s not found.", msg.Author.ID, username)
		_, err = bot.client.ChannelMessageSend(msg.ChannelID, reply)
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

	reply := fmt.Sprintf("<@%s> karma for %s: %d / %d", msg.Author.ID, res.User.Name, total, negative)
	_, err = bot.client.ChannelMessageSend(msg.ChannelID, reply)
	return err
}

func (bot *DiscordBot) separator(msg *discordgo.MessageCreate) error {
	_, err := bot.client.ChannelMessageSend(msg.ChannelID, "══════════════════════════════")
	if err != nil {
		return err
	}
	return bot.client.ChannelMessageDelete(msg.ChannelID, msg.ID)
}

func (bot *DiscordBot) isDMChannel(channelID string) (bool, error) {
	channel, err := bot.client.Channel(channelID)
	if err != nil {
		return false, err
	}
	return channel.Type == discordgo.ChannelTypeDM, nil
}

func fullAuthorName(msg *discordgo.MessageCreate) string {
	return msg.Author.Username + "#" + msg.Author.Discriminator
}
