package main

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"log"
	"math/rand"
	"regexp"
	"strings"
	"text/template"
	"time"
)

const (
	EmojiFire         string = "\U0001f525"
	EmojiThumbDown    string = "\U0001f44e"
	EmojiOkHand       string = "\U0001f44c"
	EmojiGrowingHeart string = "\U0001f497"
	EmojiOneHundred   string = "\U0001f4af"
	EmojiRainbow      string = "\U0001f308"
	EmojiPrayingHands string = "\U0001f64f"
)

type DiscordBotConf struct {
	Token      string `json:"token"`
	General    string `json:"general"`
	Log        string `json:"log"`
	HighScores string `json:"highscores"`
	Admin      string `json:"admin"`
	Prefix     string `json:"prefix"`
	Welcome    string `json:"welcome"`
}

type DiscordCommand struct {
	Command   string
	Aliases   []string
	Callback  func(DiscordMessage) error
	Admin     bool
	NoCleanUp bool
	HasArgs   bool
}

type DiscordWelcomeData struct {
	ChannelsID DiscordBotChannelsID
	Member     DiscordMember
}

type DiscordBot struct {
	logger        *log.Logger
	storage       *Storage
	client        *discordgo.Session
	Commands      []DiscordCommand
	linkReactions []string
	redditLink    *regexp.Regexp
	ChannelsID    DiscordBotChannelsID
	AdminID       string
	AddUser       chan UserQuery
	Prefix        string
	Welcome       *template.Template
}

type DiscordBotChannelsID struct {
	General    string
	Log        string
	HighScores string
}

type DiscordMember struct {
	ID            string
	Name          string
	Discriminator string
}

type DiscordMessage struct {
	Args      []string
	Author    DiscordMember
	Content   string
	ChannelID string
	IsDM      bool
	ID        string
}

func (member DiscordMember) FullyQualified() string {
	return member.Name + "#" + member.Discriminator
}

func (cmd DiscordCommand) Match(prefix, content string) (bool, string) {
	if matches, head := cmd.SingleMatch(cmd.Command, prefix, content); matches {
		return matches, strings.TrimPrefix(content, head)
	}
	for _, name := range cmd.Aliases {
		if matches, head := cmd.SingleMatch(name, prefix, content); matches {
			return matches, strings.TrimPrefix(content, head)
		}
	}
	return false, content
}

func (cmd DiscordCommand) SingleMatch(name, prefix, content string) (bool, string) {
	if cmd.HasArgs {
		head := prefix + name + " "
		if strings.HasPrefix(content, head) && len(content) > len(head) {
			return true, head
		}
	} else {
		head := prefix + name
		if head == content {
			return true, head
		}
	}
	return false, ""
}

func NewDiscordBot(storage *Storage, logger *log.Logger, conf DiscordBotConf) (*DiscordBot, error) {
	session, err := discordgo.New("Bot " + conf.Token)
	if err != nil {
		return nil, err
	}

	bot := &DiscordBot{
		client:        session,
		logger:        logger,
		storage:       storage,
		linkReactions: []string{EmojiOkHand, EmojiOneHundred, EmojiGrowingHeart, EmojiFire},
		redditLink:    regexp.MustCompile(`(?s:.*reddit\.com/r/\w+/comments/.*)`),
		AddUser:       make(chan UserQuery),
		Prefix:        conf.Prefix,
	}

	bot.Commands = bot.GetCommandsDescriptors()

	if conf.Welcome != "" {
		bot.Welcome = template.Must(template.New("DiscordWelcome").Parse(conf.Welcome))
		session.AddHandler(func(s *discordgo.Session, event *discordgo.GuildMemberAdd) {
			bot.welcomeNewMember(event.Member)
		})
	}

	session.AddHandler(func(s *discordgo.Session, msg *discordgo.MessageCreate) { bot.onMessage(msg) })
	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) { bot.onReady(conf) })

	return bot, nil
}

func (bot *DiscordBot) Run() error {
	if err := bot.client.Open(); err != nil {
		return err
	}
	return nil
}

func (bot *DiscordBot) Close() error {
	return bot.client.Close()
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
	if err := bot.client.UpdateStatus(0, "Downvote Counter"); err != nil {
		bot.logger.Fatal("Couldn't set status on discord")
	}

	if _, err := bot.client.Channel(conf.General); err != nil {
		bot.logger.Fatal(err)
	}
	bot.ChannelsID.General = conf.General

	if _, err := bot.client.Channel(conf.Log); err != nil {
		bot.logger.Fatal(err)
	}
	bot.ChannelsID.Log = conf.Log

	if conf.HighScores != "" {
		if _, err := bot.client.Channel(conf.HighScores); err != nil {
			bot.logger.Fatal(err)
		}
		bot.ChannelsID.HighScores = conf.HighScores
	}

	if _, err := bot.client.User(conf.Admin); err != nil {
		bot.logger.Fatal(err)
	}
	bot.AdminID = conf.Admin

	bot.logger.Print("Initialization ok")
}

func (bot *DiscordBot) welcomeNewMember(member *discordgo.Member) {
	var msg strings.Builder
	data := DiscordWelcomeData{
		ChannelsID: bot.ChannelsID,
		Member: DiscordMember{
			ID:            member.User.ID,
			Name:          member.User.Username,
			Discriminator: member.User.Discriminator,
		},
	}
	if err := bot.Welcome.Execute(&msg, data); err != nil {
		bot.logger.Fatal(err)
	}
	if err := bot.ChannelMessageSend(bot.ChannelsID.General, msg.String()); err != nil {
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
		ID:        dg_msg.ID,
		IsDM:      is_dm,
		Content:   dg_msg.Content,
		ChannelID: dg_msg.ChannelID,
		Author: DiscordMember{
			ID:            dg_msg.Author.ID,
			Name:          dg_msg.Author.Username,
			Discriminator: dg_msg.Author.Discriminator,
		},
	}

	if bot.isLoggableRedditLink(msg) {
		bot.logger.Print("Link to a comment on reddit posted by ", msg.Author.FullyQualified())
		err = bot.processRedditLink(msg)
	} else {
		err = bot.command(msg)
	}

	if err != nil {
		bot.logger.Print(err)
	}
}

func (bot *DiscordBot) RedditEvents(evts chan Comment) {
	for comment := range evts {
		bot.logger.Print("New event from reddit: ", comment)
		if comment.Author == "DownvoteTrollingBot" || comment.Author == "DownvoteTrollingBot2" {
			msg := "@everyone https://www.reddit.com" + comment.Permalink
			if err := bot.ChannelMessageSend(bot.ChannelsID.General, msg); err != nil {
				bot.logger.Print("Reddit events listener: ", err)
			}
		}
	}
}

func (bot *DiscordBot) SignalSuspensions(suspensions chan User) {
	for user := range suspensions {
		msg := fmt.Sprintf("RIP %s %s", user.Name, EmojiPrayingHands)
		if err := bot.ChannelMessageSend(bot.ChannelsID.General, msg); err != nil {
			bot.logger.Print("Suspensions listener: ", err)
		}
	}
}

func (bot *DiscordBot) SignalUnsuspensions(ch chan User) {
	for user := range ch {
		msg := fmt.Sprintf("%s %s has been unsuspended! %s", EmojiRainbow, user.Name, EmojiRainbow)
		if err := bot.ChannelMessageSend(bot.ChannelsID.General, msg); err != nil {
			bot.logger.Print("Unsuspensions listener: ", err)
		}
	}
}

func (bot *DiscordBot) SignalHighScores(ch chan Comment) {
	for comment := range ch {
		link := "https://www.reddit.com" + comment.Permalink
		tmpl := "A comment by %s has reached %d: %s"
		msg := fmt.Sprintf(tmpl, comment.Author, comment.Score, link)
		if err := bot.ChannelMessageSend(bot.ChannelsID.HighScores, msg); err != nil {
			bot.logger.Print("High-scores listener: ", err)
		}
	}
}

func (bot *DiscordBot) MatchCommand(msg DiscordMessage) (DiscordCommand, DiscordMessage) {
	for _, cmd := range bot.Commands {
		if cmd.Admin && msg.Author.ID != bot.AdminID {
			continue
		}
		if matches, content_rest := cmd.Match(bot.Prefix, msg.Content); matches {
			msg.Content = content_rest
			msg.Args = strings.Split(msg.Content, " ")
			return cmd, msg
		}
	}
	return DiscordCommand{}, msg
}

func (bot *DiscordBot) command(msg DiscordMessage) error {
	cmd, msg := bot.MatchCommand(msg)

	if cmd.Command == "" {
		return nil
	}

	err := cmd.Callback(msg)

	if err == nil && !cmd.NoCleanUp && !msg.IsDM {
		time.Sleep(5 * time.Second)
		err = bot.client.ChannelMessageDelete(msg.ChannelID, msg.ID)
	}

	return err
}

func (bot *DiscordBot) isLoggableRedditLink(msg DiscordMessage) bool {
	return (msg.ChannelID == bot.ChannelsID.General &&
		bot.redditLink.MatchString(msg.Content) &&
		!strings.Contains(strings.ToLower(msg.Content), "!nolog"))
}

func (bot *DiscordBot) processRedditLink(msg DiscordMessage) error {
	if err := bot.addRandomReactionTo(msg); err != nil {
		return err
	}
	reply := msg.Author.FullyQualified() + ": " + msg.Content
	return bot.ChannelMessageSend(bot.ChannelsID.Log, reply)
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
			Command:  "karma",
			Callback: bot.karma,
			HasArgs:  true,
		},
		DiscordCommand{
			Command:  "ping",
			Callback: bot.simpleReply("pong"),
			Admin:    true,
		},
		DiscordCommand{
			Command:  "register",
			Callback: bot.register,
			HasArgs:  true,
		},
		DiscordCommand{
			Command:  "unregister",
			Callback: bot.editUsers("unregister", bot.storage.DelUser),
			HasArgs:  true,
			Admin:    true,
		},
		DiscordCommand{
			Command:  "purge",
			Callback: bot.editUsers("purge", bot.storage.PurgeUser),
			HasArgs:  true,
			Admin:    true,
		},
		DiscordCommand{
			Command:  "exists",
			Callback: bot.userExists,
			HasArgs:  true,
		},
		DiscordCommand{
			Command: "sip",
			Aliases: []string{"sipthebep"},
			Callback: bot.simpleReply(fmt.Sprintf(
				"More like N0000 1 cares %s This shitpost is horrible %s",
				EmojiFire, strings.Repeat(EmojiThumbDown, 3))),
		},
		DiscordCommand{
			Command:  "separator",
			Aliases:  []string{"sep", "="},
			Callback: bot.simpleReply("══════════════════"),
		},
	}
}

func (bot *DiscordBot) simpleReply(reply string) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		return bot.ChannelMessageSend(msg.ChannelID, reply)
	}
}

func (bot *DiscordBot) register(msg DiscordMessage) error {
	names := msg.Args
	bot.logger.Printf("%s wants to register %v", msg.Author.FullyQualified(), names)

	statuses := make([]string, 0, len(names))
	for _, name := range names {
		if err := bot.client.ChannelTyping(msg.ChannelID); err != nil {
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
	// TODO post multiple messages instead
	if len(status) > 1900 {
		status = "registrations done, check the logs for more details."
	}
	response := fmt.Sprintf("<@%s> registration: %s", msg.Author.ID, status)
	return bot.ChannelMessageSend(msg.ChannelID, response)
}

func (bot *DiscordBot) editUsers(action_name string, action func(string) error) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		names := msg.Args
		bot.logger.Printf("%s wants to %s %v", msg.Author.FullyQualified(), action_name, names)

		results := make([]string, len(names))
		for i, name := range names {
			if err := action(name); err != nil {
				results[i] = fmt.Sprintf("%s: error %s", name, err)
			} else {
				results[i] = fmt.Sprintf("%s: ok", name)
			}
		}

		result := strings.Join(results, ", ")
		response := fmt.Sprintf("<@%s> %s: %s", msg.Author.ID, action_name, result)
		return bot.ChannelMessageSend(msg.ChannelID, response)
	}
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
			status = "found"
			break
		}
	}

	response := fmt.Sprintf("<@%s> User %s %s.", msg.Author.ID, username, status)
	return bot.ChannelMessageSend(msg.ChannelID, response)
}

func (bot *DiscordBot) karma(msg DiscordMessage) error {
	username := msg.Content
	if err := bot.client.ChannelTyping(msg.ChannelID); err != nil {
		return err
	}

	res := bot.storage.GetUser(username)
	if res.Error != nil {
		return res.Error
	}

	if !res.Exists {
		reply := fmt.Sprintf("<@%s> user %s not found.", msg.Author.ID, username)
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

	reply := fmt.Sprintf("<@%s> karma for %s: %d / %d", msg.Author.ID, res.User.Name, total, negative)
	return bot.ChannelMessageSend(msg.ChannelID, reply)
}
