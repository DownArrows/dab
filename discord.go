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

type DiscordBotConf struct {
	Token      string `json:"token"`
	General    string `json:"general"`
	Log        string `json:"log"`
	HighScores string `json:"highscores"`
	Admin      string `json:"admin"`
	Prefix     string `json:"prefix"`
}

type DiscordBot struct {
	logger        *log.Logger
	storage       *Storage
	client        *discordgo.Session
	Commands      []DiscordCommand
	linkReactions []string
	redditLink    *regexp.Regexp
	Channels      struct {
		General    *discordgo.Channel
		Log        *discordgo.Channel
		HighScores *discordgo.Channel
	}
	admin   *discordgo.User
	AddUser chan UserQuery
	Prefix  string
}

type DiscordMessage struct {
	Args       []string
	Content    string
	AuthorName string
	AuthorID   string
	ChannelID  string
	IsDM       bool
	FQAuthor   string // Fully Qualified Author (Name)
	ID         string
}

type DiscordCommand struct {
	Command    string
	Aliases    []string
	Callback   func(DiscordMessage) error
	Admin      bool
	AutoDelete bool
	NoArgs     bool
}

func (cmd DiscordCommand) Match(prefix, content string) (bool, string) {
	head := prefix + cmd.Command
	if !cmd.NoArgs {
		head += " "
	}
	if strings.HasPrefix(head, content) {
		return true, strings.TrimPrefix(head, content)
	}
	for _, name := range cmd.Aliases {
		head := prefix + name
		if !cmd.NoArgs {
			head += " "
		}
		if strings.HasPrefix(head, content) {
			return true, strings.TrimPrefix(head, content)
		}
	}
	return false, content
}

func NewDiscordBot(storage *Storage, logOut io.Writer, conf DiscordBotConf) (*DiscordBot, error) {
	logger := log.New(logOut, "discordbot: ", log.LstdFlags)

	session, err := discordgo.New("Bot " + conf.Token)
	if err != nil {
		return nil, err
	}

	bot := &DiscordBot{
		client:        session,
		logger:        logger,
		storage:       storage,
		linkReactions: []string{"👌", "💗", "🔥", "💯"},
		redditLink:    regexp.MustCompile(`(?s:.*reddit\.com/r/\w+/comments/.*)`),
		AddUser:       make(chan UserQuery),
		Prefix:        conf.Prefix,
	}
	bot.Commands = bot.GetCommandsDescriptors()

	session.AddHandler(func(s *discordgo.Session, msg *discordgo.MessageCreate) {
		bot.onMessage(msg)
	})

	session.AddHandler(func(s *discordgo.Session, event *discordgo.GuildMemberAdd) {
		bot.onNewMember(event.Member)
	})

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		bot.onReady(conf)
	})

	return bot, nil
}

func (bot *DiscordBot) Run() error {
	err := bot.client.Open()
	if err != nil {
		return err
	}
	go bot.setPlayingStatus()
	return nil
}

func (bot *DiscordBot) Close() error {
	return bot.client.Close()
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

func (bot *DiscordBot) isDMChannel(channelID string) (bool, error) {
	channel, err := bot.client.Channel(channelID)
	if err != nil {
		return false, err
	}
	return channel.Type == discordgo.ChannelTypeDM, nil
}

func (bot *DiscordBot) ChannelMessageSend(channelID, content string) error {
	_, err := bot.client.ChannelMessageSend(channelID, content)
	return err
}

func (bot *DiscordBot) onReady(conf DiscordBotConf) {
	var err error
	bot.Channels.General, err = bot.client.Channel(conf.General)
	bot.fatal(err)

	bot.Channels.Log, err = bot.client.Channel(conf.Log)
	bot.fatal(err)

	if conf.HighScores != "" {
		bot.Channels.HighScores, err = bot.client.Channel(conf.HighScores)
		bot.fatal(err)
	}

	bot.admin, err = bot.client.User(conf.Admin)
	bot.fatal(err)
	bot.logger.Print("Initialization ok")
}

func (bot *DiscordBot) fatal(err error) {
	if err != nil {
		bot.logger.Fatal(err)
	}
}

func (bot *DiscordBot) onNewMember(member *discordgo.Member) {
	manual := "397034210218475520"
	welcome := "Hello <@%s>! Have a look at the <#%s> to understand what's going on here, and don't hesitate to post on <#%s> and to try new things we haven't thought of! This server is still rather new and experimental, but we think it has great potential. We may have some knowledge of the craft to share too."
	msg := fmt.Sprintf(welcome, member.User.ID, manual, bot.Channels.General.ID)
	err := bot.ChannelMessageSend(bot.Channels.General.ID, msg)
	if err != nil {
		bot.logger.Print(err)
	}
}

func (bot *DiscordBot) onMessage(dg_msg *discordgo.MessageCreate) {
	var err error

	if dg_msg.Author.ID == bot.client.State.User.ID {
		return
	}

	is_dm, err := bot.isDMChannel(dg_msg.ChannelID)
	if err != nil {
		bot.logger.Print(err)
	}

	msg := DiscordMessage{
		Content:   dg_msg.Content,
		AuthorID:  dg_msg.Author.ID,
		ChannelID: dg_msg.ChannelID,
		IsDM:      is_dm,
		FQAuthor:  dg_msg.Author.Username + "#" + dg_msg.Author.Discriminator,
		ID:        dg_msg.ID,
	}

	if bot.isLoggableRedditLink(msg) {
		bot.logger.Print("Link to a comment on reddit posted by ", msg.FQAuthor)
		err = bot.processRedditLink(msg)
	} else {
		err = bot.command(msg)
	}

	if err != nil {
		bot.logger.Print(err)
	}
}

func (bot *DiscordBot) RedditEvents(evts chan Comment) {
	var err error
	for comment := range evts {
		bot.logger.Print("New event from reddit: ", comment)

		if comment.Author == "DownvoteTrollingBot" || comment.Author == "DownvoteTrollingBot2" {
			msg := "@everyone https://www.reddit.com" + comment.Permalink
			err = bot.ChannelMessageSend(bot.Channels.General.ID, msg)
		}

		if err != nil {
			bot.logger.Print("Reddit events listener: ", err)
		}
	}
}

func (bot *DiscordBot) SignalSuspensions(suspensions chan User) {
	for user := range suspensions {
		msg := fmt.Sprintf("RIP %s 🙏", user.Name)
		err := bot.ChannelMessageSend(bot.Channels.General.ID, msg)
		if err != nil {
			bot.logger.Print("Suspensions listener: ", err)
		}
	}
}

func (bot *DiscordBot) SignalUnsuspensions(ch chan User) {
	for user := range ch {
		msg := fmt.Sprintf("🌈 %s has been unsuspended! 🌈", user.Name)
		err := bot.ChannelMessageSend(bot.Channels.General.ID, msg)
		if err != nil {
			bot.logger.Print("Unsuspensions listener: ", err)
		}
	}
}

func (bot *DiscordBot) SignalHighScores(ch chan Comment) {
	for comment := range ch {
		link := "https://www.reddit.com" + comment.Permalink
		tmpl := "A comment by %s has reached %d: %s"
		msg := fmt.Sprintf(tmpl, comment.Author, comment.Score, link)
		err := bot.ChannelMessageSend(bot.Channels.HighScores.ID, msg)
		if err != nil {
			bot.logger.Print("High-scores listener: ", err)
		}
	}
}

func (bot *DiscordBot) command(msg DiscordMessage) error {
	var cmd DiscordCommand
	for _, a_cmd := range bot.Commands {
		if a_cmd.Admin && msg.AuthorID != bot.admin.ID {
			continue
		}
		if matches, content_rest := a_cmd.Match(bot.Prefix, msg.Content); matches {
			msg.Content = content_rest
			msg.Args = strings.Split(msg.Content, " ")
			cmd = a_cmd
			break
		}
	}

	if cmd.Command == "" {
		return nil
	}

	err := cmd.Callback(msg)

	if err == nil && cmd.AutoDelete && !msg.IsDM {
		time.Sleep(5 * time.Second)
		err = bot.client.ChannelMessageDelete(msg.ChannelID, msg.ID)
	}

	return err
}

func (bot *DiscordBot) isLoggableRedditLink(msg DiscordMessage) bool {
	return (msg.ChannelID == bot.Channels.General.ID &&
		bot.redditLink.MatchString(msg.Content) &&
		!strings.Contains(strings.ToLower(msg.Content), "!nolog"))
}

func (bot *DiscordBot) processRedditLink(msg DiscordMessage) error {
	err := bot.addRandomReactionTo(msg)
	if err != nil {
		return err
	}
	reply := msg.FQAuthor + ": " + msg.Content
	return bot.ChannelMessageSend(bot.Channels.Log.ID, reply)
}

func (bot *DiscordBot) addRandomReactionTo(msg DiscordMessage) error {
	nb_reactions := len(bot.linkReactions)
	rand_index := rand.Int31n(int32(nb_reactions))
	reaction := bot.linkReactions[rand_index]
	return bot.client.MessageReactionAdd(msg.ChannelID, msg.ID, reaction)
}

func (bot *DiscordBot) GetCommandsDescriptors() []DiscordCommand {
	return []DiscordCommand{
		DiscordCommand{
			Command:    "karma",
			Callback:   bot.karma,
			AutoDelete: true,
		},
		DiscordCommand{
			Command:    "ping",
			Callback:   bot.pong,
			AutoDelete: true,
			Admin:      true,
			NoArgs:     true,
		},
		DiscordCommand{
			Command:    "register",
			Callback:   bot.register,
			AutoDelete: true,
		},
		DiscordCommand{
			Command:    "unregister",
			Callback:   bot.unregister,
			AutoDelete: true,
			Admin:      true,
		},
		DiscordCommand{
			Command:    "purge",
			Callback:   bot.purge,
			AutoDelete: true,
			Admin:      true,
		},
		DiscordCommand{
			Command:    "exists",
			Callback:   bot.userExists,
			AutoDelete: true,
		},
		DiscordCommand{
			Command:    "sip",
			Aliases:    []string{"sipthebep"},
			Callback:   bot.sipTheBep,
			AutoDelete: true,
			NoArgs:     true,
		},
		DiscordCommand{
			Command:    "separator",
			Aliases:    []string{"sep", "="},
			Callback:   bot.separator,
			AutoDelete: false,
			NoArgs:     true,
		},
	}
}

func (bot *DiscordBot) pong(msg DiscordMessage) error {
	return bot.ChannelMessageSend(msg.ChannelID, "pong")
}

func (bot *DiscordBot) register(msg DiscordMessage) error {
	names := msg.Args
	bot.logger.Print(msg.FQAuthor, " wants to register ", names)

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
	response := fmt.Sprintf("<@%s> registration: %s", msg.AuthorID, status)
	return bot.ChannelMessageSend(msg.ChannelID, response)
}

func (bot *DiscordBot) unregister(msg DiscordMessage) error {
	names := msg.Args
	bot.logger.Print(msg.FQAuthor, " wants to unregister ", names)

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
	response := fmt.Sprintf("<@%s> unregister: %s", msg.AuthorID, result)
	return bot.ChannelMessageSend(msg.ChannelID, response)
}

func (bot *DiscordBot) purge(msg DiscordMessage) error {
	names := msg.Args
	bot.logger.Print(msg.FQAuthor, " wants to purge ", names)

	results := make([]string, len(names))
	for i, name := range names {
		err := bot.storage.PurgeUser(name)
		if err != nil {
			results[i] = fmt.Sprintf("%s: error %s", name, err)
		} else {
			results[i] = fmt.Sprintf("%s: ok", name)
		}
	}

	result := strings.Join(results, ", ")
	response := fmt.Sprintf("<@%s> purge: %s", msg.AuthorID, result)
	return bot.ChannelMessageSend(msg.ChannelID, response)
}

func (bot *DiscordBot) userExists(msg DiscordMessage) error {
	username := msg.Content

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

	response := fmt.Sprintf("<@%s> User %s %s.", msg.AuthorID, username, status)
	return bot.ChannelMessageSend(msg.ChannelID, response)
}

func (bot *DiscordBot) karma(msg DiscordMessage) error {
	username := msg.Content
	err := bot.client.ChannelTyping(msg.ChannelID)
	if err != nil {
		return err
	}

	res := bot.storage.GetUser(username)
	if res.Error != nil {
		return res.Error
	}

	if !res.Exists {
		reply := fmt.Sprintf("<@%s> user %s not found.", msg.AuthorID, username)
		return bot.ChannelMessageSend(msg.ChannelID, reply)
	}

	total, err := bot.storage.GetTotalKarma(username)
	if err != nil {
		return err
	}

	negative, err := bot.storage.GetNegativeKarma(username)
	if err != nil {
		return err
	}

	reply := fmt.Sprintf("<@%s> karma for %s: %d / %d", msg.AuthorID, res.User.Name, total, negative)
	return bot.ChannelMessageSend(msg.ChannelID, reply)
}

func (bot *DiscordBot) sipTheBep(msg DiscordMessage) error {
	response := `More like N0000 1 cares 🔥 This shitpost is horrible 👎👎👎`
	return bot.ChannelMessageSend(msg.ChannelID, response)
}

func (bot *DiscordBot) separator(msg DiscordMessage) error {
	return bot.ChannelMessageSend(msg.ChannelID, "══════════════════")
}
