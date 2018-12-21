package main

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"text/template"
	"time"
)

const (
	EmojiCheckMark     = "\u2705"
	EmojiCrossBones    = "\u2620"
	EmojiCrossMark     = "\u274c"
	EmojiFire          = "\U0001f525"
	EmojiGrowingHeart  = "\U0001f497"
	EmojiHighVoltage   = "\u26a1"
	EmojiOkHand        = "\U0001f44c"
	EmojiOneHundred    = "\U0001f4af"
	EmojiPrayingHands  = "\U0001f64f"
	EmojiRainbow       = "\U0001f308"
	EmojiThumbDown     = "\U0001f44e"
	EmojiThumbsUp      = "\U0001f44d"
	EmojiWheelOfDharma = "\u2638"
	EmojiWhiteFlower   = "\U0001f4ae"
)

const DiscordMessageLengthLimit = 2000

var linkReactions = []string{
	EmojiCrossBones, EmojiFire, EmojiGrowingHeart, EmojiHighVoltage,
	EmojiOkHand, EmojiOneHundred, EmojiThumbsUp, EmojiWhiteFlower,
}

func embedField(name, value string, inline bool) *discordgo.MessageEmbedField {
	return &discordgo.MessageEmbedField{
		Name:   name,
		Value:  value,
		Inline: inline,
	}
}

func embedAddField(embed *discordgo.MessageEmbed, name, value string, inline bool) {
	embed.Fields = append(embed.Fields, embedField(name, value, inline))
}

type DiscordMessage struct {
	Args      []string
	Author    DiscordMember
	Content   string
	ChannelID string
	IsDM      bool
	ID        string
}

type DiscordMember struct {
	ID            string
	Name          string
	Discriminator string
}

func (member DiscordMember) FQN() string {
	return member.Name + "#" + member.Discriminator
}

type DiscordCommand struct {
	Command   string
	Aliases   []string
	Callback  func(DiscordMessage) error
	Admin     bool
	NoCleanUp bool
	HasArgs   bool
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

type DiscordWelcomeData struct {
	ChannelsID DiscordBotChannelsID
	Member     DiscordMember
}

type DiscordBotChannelsID struct {
	General    string `json:"general"`
	Log        string `json:"log"`
	HighScores string `json:"highscores"`
}

type DiscordBot struct {
	// dependencies
	client  *discordgo.Session
	logger  LevelLogger
	storage DiscordBotStorage

	// configuration
	adminID    string
	channelsID DiscordBotChannelsID
	guildID    string
	hidePrefix string
	prefix     string
	timezone   *time.Location
	welcome    *template.Template

	// miscellaneous
	AddUser    chan UserQuery
	commands   []DiscordCommand
	done       chan error
	redditLink *regexp.Regexp
}

func NewDiscordBot(storage DiscordBotStorage, logger LevelLogger, conf DiscordBotConf) (*DiscordBot, error) {
	session, err := discordgo.New("Bot " + conf.Token)
	if err != nil {
		return nil, err
	}

	welcome, err := template.New("DiscordWelcome").Parse(conf.Welcome) // works even if empty
	if err != nil {
		return nil, err
	}

	bot := &DiscordBot{
		client:  session,
		logger:  logger,
		storage: storage,

		adminID:    conf.Admin,
		channelsID: conf.DiscordBotChannelsID,
		hidePrefix: conf.HidePrefix,
		prefix:     conf.Prefix,
		timezone:   conf.Timezone.Value,
		welcome:    welcome,

		AddUser:    make(chan UserQuery),
		done:       make(chan error),
		redditLink: regexp.MustCompile(`(?s:.*reddit\.com/r/\w+/comments/.*)`),
	}

	bot.commands = bot.getCommandsDescriptors()

	if conf.Welcome != "" || conf.General != "" {
		session.AddHandler(func(s *discordgo.Session, event *discordgo.GuildMemberAdd) {
			bot.welcomeNewMember(event.Member)
		})
	}

	session.AddHandler(func(s *discordgo.Session, msg *discordgo.MessageCreate) { bot.onMessage(msg) })
	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) { bot.onReady() })

	return bot, nil
}

func (bot *DiscordBot) Run(ctx context.Context) error {
	go func() {
		if err := bot.client.Open(); err != nil {
			bot.done <- err
		}
	}()

	defer bot.client.Close()

	select {
	case err := <-bot.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (bot *DiscordBot) fatal(err error) {
	bot.logger.Errorf("fatal: %v", err)
	bot.done <- err
}

func (bot *DiscordBot) isDMChannel(channelID string) (bool, error) {
	channel, err := bot.client.Channel(channelID)
	if err != nil {
		return false, err
	}
	return channel.Type == discordgo.ChannelTypeDM, nil
}

func (bot *DiscordBot) channelMessageSend(channelID, content string) error {
	_, err := bot.client.ChannelMessageSend(channelID, content)
	return err
}

func (bot *DiscordBot) channelEmbedSend(channelID string, embed *discordgo.MessageEmbed) error {
	_, err := bot.client.ChannelMessageSendEmbed(channelID, embed)
	return err
}

func (bot *DiscordBot) myColor(channelID string) int {
	return bot.client.State.UserColor(bot.client.State.User.ID, channelID)
}

// this is executed on each (re)-connection to Discord
func (bot *DiscordBot) onReady() {
	bot.logger.Debug("(re-)connected to discord, checking settings")
	if err := bot.client.UpdateStatus(0, "Downvote Counter"); err != nil {
		bot.fatal(fmt.Errorf("couldn't set status on discord: %v", err))
		return
	}

	// Check the channels exist and are all in the same server
	channels := reflect.ValueOf(bot.channelsID)
	for i := 0; i < channels.NumField(); i++ {
		channelID := channels.Field(i).String()
		name := channels.Type().Field(i).Name

		if channelID == "" {
			continue
		}

		channel, err := bot.client.Channel(channelID)
		if err != nil {
			bot.fatal(fmt.Errorf("channel %s: %v", name, err))
			return
		}

		if bot.guildID == "" {
			bot.guildID = channel.GuildID
		} else if bot.guildID != channel.GuildID {
			bot.fatal(fmt.Errorf("all channels must be in the same server: channel %s is not in server %s", name, bot.guildID))
			return
		}
	}

	if bot.adminID == "" {
		if bot.guildID == "" {
			bot.logger.Info("no channel and no admin set; disabling privileged commands")
		} else {
			guild, err := bot.client.Guild(bot.guildID)
			if err != nil {
				bot.fatal(err)
				return
			}
			bot.adminID = guild.OwnerID
		}
	} else if _, err := bot.client.User(bot.adminID); err != nil {
		bot.fatal(err)
		return
	}
	bot.logger.Debugf("privileged user has ID %s", bot.adminID)
}

func (bot *DiscordBot) welcomeNewMember(member *discordgo.Member) {
	var msg strings.Builder
	data := DiscordWelcomeData{
		ChannelsID: bot.channelsID,
		Member: DiscordMember{
			ID:            member.User.ID,
			Name:          member.User.Username,
			Discriminator: member.User.Discriminator,
		},
	}
	bot.logger.Debugf("welcoming user %s", data.Member.FQN())
	if err := bot.welcome.Execute(&msg, data); err != nil {
		bot.fatal(err)
	}
	if err := bot.channelMessageSend(bot.channelsID.General, msg.String()); err != nil {
		bot.logger.Error(err)
	}
}

func (bot *DiscordBot) onMessage(dg_msg *discordgo.MessageCreate) {
	var err error

	if dg_msg.Author.ID == bot.client.State.User.ID {
		return
	}

	is_dm, err := bot.isDMChannel(dg_msg.ChannelID)
	if err != nil {
		bot.logger.Error(err)
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
		err = bot.processRedditLink(msg)
	} else {
		err = bot.command(msg)
	}

	if err != nil {
		bot.logger.Error(err)
	}
}

func (bot *DiscordBot) SignalNewRedditPosts(ctx context.Context, evts <-chan Comment) error {
Loop:
	for {
		select {
		case comment := <-evts:
			bot.logger.Infof("new post on sub %s by %s on %s", comment.Sub, comment.Author, time.Unix(comment.Created, 0))
			if comment.Author == "DownvoteTrollingBot" || comment.Author == "DownvoteTrollingBot2" {
				msg := "@everyone https://www.reddit.com" + comment.Permalink
				if err := bot.channelMessageSend(bot.channelsID.General, msg); err != nil {
					bot.logger.Errorf("error when signaling new post on reddit: %v", err)
				}
			}
		case <-ctx.Done():
			break Loop
		}
	}
	return ctx.Err()
}

func (bot *DiscordBot) SignalSuspensions(ctx context.Context, suspensions <-chan User) error {
Loop:
	for {
		select {
		case user := <-suspensions:
			state := "suspended"
			if user.NotFound {
				state = "deleted"
			}
			msg := fmt.Sprintf("RIP /u/%s %s (%s)", user.Name, EmojiPrayingHands, state)
			if err := bot.channelMessageSend(bot.channelsID.General, msg); err != nil {
				bot.logger.Errorf("error when signaling a suspension or deletion: %v", err)
			}
		case <-ctx.Done():
			break Loop
		}
	}
	return ctx.Err()
}

func (bot *DiscordBot) SignalUnsuspensions(ctx context.Context, ch <-chan User) error {
Loop:
	for {
		select {
		case user := <-ch:
			msg := fmt.Sprintf("%s /u/%s has been unsuspended! %s", EmojiRainbow, user.Name, EmojiRainbow)
			if err := bot.channelMessageSend(bot.channelsID.General, msg); err != nil {
				bot.logger.Errorf("error when signaling an unsuspensions: %v", err)
			}
		case <-ctx.Done():
			break Loop
		}
	}
	return ctx.Err()
}

func (bot *DiscordBot) SignalHighScores(ctx context.Context, ch <-chan Comment) error {
Loop:
	for {
		select {
		case comment := <-ch:
			link := "https://www.reddit.com" + comment.Permalink
			tmpl := "a comment by /u/%s has reached %d: %s"
			msg := fmt.Sprintf(tmpl, comment.Author, comment.Score, link)
			if err := bot.channelMessageSend(bot.channelsID.HighScores, msg); err != nil {
				bot.logger.Errorf("error when signaling high-score: %v", err)
			}
		case <-ctx.Done():
			break Loop
		}
	}
	return ctx.Err()
}

func (bot *DiscordBot) matchCommand(msg DiscordMessage) (DiscordCommand, DiscordMessage) {
	for _, cmd := range bot.commands {
		if cmd.Admin && msg.Author.ID != bot.adminID {
			continue
		}
		if matches, content_rest := cmd.Match(bot.prefix, msg.Content); matches {
			msg.Content = content_rest
			msg.Args = strings.Split(msg.Content, " ")
			return cmd, msg
		}
	}
	return DiscordCommand{}, msg
}

func (bot *DiscordBot) command(msg DiscordMessage) error {
	cmd, msg := bot.matchCommand(msg)
	if cmd.Command == "" {
		return nil
	}
	bot.logger.Debugf("matched command '%s', args %v, from user %s", cmd.Command, msg.Args, msg.Author.FQN())

	if err := bot.client.ChannelTyping(msg.ChannelID); err != nil {
		return err
	}

	err := cmd.Callback(msg)

	if err == nil && !cmd.NoCleanUp && !msg.IsDM {
		time.Sleep(5 * time.Second)
		err = bot.client.ChannelMessageDelete(msg.ChannelID, msg.ID)
	}

	return err
}

func (bot *DiscordBot) isLoggableRedditLink(msg DiscordMessage) bool {
	return (msg.ChannelID == bot.channelsID.General && // won't be true if General is not set (ie left empty)
		bot.redditLink.MatchString(msg.Content) &&
		!strings.Contains(strings.ToLower(msg.Content), "!nolog"))
}

func (bot *DiscordBot) processRedditLink(msg DiscordMessage) error {
	if err := bot.addRandomReactionTo(msg); err != nil {
		return err
	}
	if bot.channelsID.Log == "" {
		return nil
	}
	reply := msg.Author.FQN() + ": " + msg.Content
	return bot.channelMessageSend(bot.channelsID.Log, reply)
}

func (bot *DiscordBot) addRandomReactionTo(msg DiscordMessage) error {
	nb_reactions := len(linkReactions)
	rand_index := rand.Int31n(int32(nb_reactions))
	reaction := linkReactions[rand_index]
	return bot.client.MessageReactionAdd(msg.ChannelID, msg.ID, reaction)
}

func (bot *DiscordBot) getCommandsDescriptors() []DiscordCommand {
	return []DiscordCommand{{
		Command:  "karma",
		Callback: bot.karma,
		HasArgs:  true,
	}, {
		Command:  "version",
		Callback: bot.simpleReply(Version.String()),
		Admin:    true,
	}, {
		Command:  "register",
		Callback: bot.register,
		HasArgs:  true,
	}, {
		Command:  "unregister",
		Callback: bot.editUsers("unregister", bot.storage.DelUser),
		HasArgs:  true,
		Admin:    true,
	}, {
		Command:  "purge",
		Callback: bot.editUsers("purge", bot.storage.PurgeUser),
		HasArgs:  true,
		Admin:    true,
	}, {
		Command:  "info",
		Callback: bot.userInfo,
		HasArgs:  true,
	}, {
		Command:  "hide",
		Callback: bot.editUsers("hide", bot.storage.HideUser),
		HasArgs:  true,
	}, {
		Command:  "unhide",
		Callback: bot.editUsers("unhide", bot.storage.UnHideUser),
		HasArgs:  true,
	}, {
		Command: "sip",
		Aliases: []string{"sipthebep"},
		Callback: bot.simpleReply(fmt.Sprintf(
			"More like N0000 1 cares %s This shitpost is horrible %s",
			EmojiFire, strings.Repeat(EmojiThumbDown, 3))),
	}, {
		Command:  "separator",
		Aliases:  []string{"sep", "="},
		Callback: bot.simpleReply("══════════════════"),
	}}
}

func (bot *DiscordBot) simpleReply(reply string) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		return bot.channelMessageSend(msg.ChannelID, reply)
	}
}

func (bot *DiscordBot) register(msg DiscordMessage) error {
	names := msg.Args
	bot.logger.Infof("%s wants to register %v", msg.Author.FQN(), names)

	status := &discordgo.MessageEmbed{
		Title:       "Registration",
		Description: fmt.Sprintf("request from <@%s>", msg.Author.ID),
		Color:       bot.myColor(msg.ChannelID),
		Fields:      []*discordgo.MessageEmbedField{},
	}

	for _, name := range names {
		if err := bot.client.ChannelTyping(msg.ChannelID); err != nil {
			return err
		}

		hidden := strings.HasPrefix(name, bot.hidePrefix)
		name = TrimUsername(strings.TrimPrefix(name, bot.hidePrefix))

		bot.AddUser <- UserQuery{User: User{Name: name, Hidden: hidden}}
		reply := <-bot.AddUser

		if IsCancellation(reply.Error) {
			continue
		} else if reply.Error != nil {
			embedAddField(status, reply.User.Name, fmt.Sprintf("%s %s", EmojiCrossMark, reply.Error), false)
		} else if !reply.Exists {
			embedAddField(status, reply.User.Name, EmojiCrossMark+" not found", false)
		} else {
			embedAddField(status, reply.User.Name, EmojiCheckMark, false)
		}
	}

	return bot.channelEmbedSend(msg.ChannelID, status)
}

func (bot *DiscordBot) editUsers(action_name string, action func(string) error) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		names := msg.Args
		bot.logger.Infof("%s wants to %s %v", msg.Author.FQN(), action_name, names)

		status := &discordgo.MessageEmbed{
			Title:       strings.Title(action_name),
			Description: fmt.Sprintf("request from <@%s>", msg.Author.ID),
			Color:       bot.myColor(msg.ChannelID),
			Fields:      []*discordgo.MessageEmbedField{},
		}

		for _, name := range names {
			name = TrimUsername(name)
			if err := action(name); err != nil {
				embedAddField(status, name, fmt.Sprintf("%s %s", EmojiCrossMark, err), false)
			} else {
				embedAddField(status, name, EmojiCheckMark, false)
			}
		}

		return bot.channelEmbedSend(msg.ChannelID, status)
	}
}

func (bot *DiscordBot) userInfo(msg DiscordMessage) error {
	username := TrimUsername(msg.Content)

	query := bot.storage.GetUser(username)

	if !query.Exists {
		response := fmt.Sprintf("<@%s> user '%s' not found in the database.", msg.Author.ID, username)
		return bot.channelMessageSend(msg.ChannelID, response)
	}

	user := query.User
	embed := &discordgo.MessageEmbed{
		Title: "Information about /u/" + user.Name,
		Color: bot.myColor(msg.ChannelID),
		Fields: []*discordgo.MessageEmbedField{
			embedField("Created", user.CreatedTime().In(bot.timezone).Format(time.RFC850), true),
			embedField("Added", user.AddedTime().In(bot.timezone).Format(time.RFC850), true),
		},
	}

	embedAddField(embed, "Hidden from reports", fmt.Sprintf("%t", user.Hidden), true)
	embedAddField(embed, "Suspended", fmt.Sprintf("%t", user.Suspended), true)
	embedAddField(embed, "Inactive", fmt.Sprintf("%t", user.Inactive), true)
	if user.LastScan > 0 {
		embedAddField(embed, "Last scan", user.LastScanTime().In(bot.timezone).Format(time.RFC850), true)
	}

	return bot.channelEmbedSend(msg.ChannelID, embed)

}

func (bot *DiscordBot) karma(msg DiscordMessage) error {
	username := TrimUsername(msg.Content)

	res := bot.storage.GetUser(username)
	if res.Error != nil {
		return res.Error
	}

	if !res.Exists {
		reply := fmt.Sprintf("<@%s> user %s not found.", msg.Author.ID, username)
		return bot.channelMessageSend(msg.ChannelID, reply)
	}

	embed := &discordgo.MessageEmbed{
		Title:  "Karma for /u/" + res.User.Name,
		Color:  bot.client.State.UserColor(bot.client.State.User.ID, msg.ChannelID),
		Fields: []*discordgo.MessageEmbedField{},
	}

	var positive int64
	var negative int64
	var err error

	positive, err = bot.storage.GetPositiveKarma(username)
	if err == ErrNoComment {
		embedAddField(embed, "Positive", "no comment with a positive score", true)
	} else if err != nil {
		return err
	} else {
		embedAddField(embed, "Positive", fmt.Sprintf("%d", positive), true)
	}

	negative, err = bot.storage.GetNegativeKarma(username)
	if err == ErrNoComment {
		embedAddField(embed, "Negative", "no comment with negative score", true)
	} else if err != nil {
		return err
	} else {
		embedAddField(embed, "Negative", fmt.Sprintf("%d", negative), true)
	}

	embedAddField(embed, "Total", fmt.Sprintf("%d", positive+negative), true)
	return bot.channelEmbedSend(msg.ChannelID, embed)
}

func TrimUsername(username string) string {
	return strings.TrimPrefix(strings.TrimPrefix(username, "/u/"), "u/")
}
