package main

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"
)

// Emojis that are used in the application.
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
	EmojiThumbUp       = "\U0001f44d"
	EmojiWarning       = "\u26a0"
	EmojiWheelOfDharma = "\u2638"
	EmojiWhiteFlower   = "\U0001f4ae"
)

// Knowledge about Discord
const (
	DiscordMessageLengthLimit = 2000
	DiscordDefaultRoleColor   = 0
)

const discordMessageDeletionWait = 15 * time.Second

var linkReactions = []string{
	EmojiCrossBones, EmojiFire, EmojiGrowingHeart, EmojiHighVoltage,
	EmojiOkHand, EmojiOneHundred, EmojiThumbUp, EmojiWhiteFlower,
}

// DiscordMessage exists because discordgo's data structures aren't well adapted to our needs,
// and typing "*discordgo.<DataStructure>" all the time gets tiring.
type DiscordMessage struct {
	Args      []string
	Author    DiscordMember
	Content   string
	ChannelID string
	IsDM      bool
	ID        string
}

// NewDiscordMessage creates a newe DiscordMessage from a *discordgo.MessageCreate
func NewDiscordMessage(dgMsg *discordgo.MessageCreate) DiscordMessage {
	return DiscordMessage{
		ID:        dgMsg.ID,
		Content:   dgMsg.Content,
		ChannelID: dgMsg.ChannelID,
		Author: DiscordMember{
			ID:            dgMsg.Author.ID,
			Name:          dgMsg.Author.Username,
			Discriminator: dgMsg.Author.Discriminator,
		},
	}
}

// DiscordMember usefully subsumes discordgo.Member and discordgo.User
type DiscordMember struct {
	ID            string
	Name          string
	Discriminator string
}

// FQN returns the fully qualified name of a user, with its discriminator.
func (member DiscordMember) FQN() string {
	return member.Name + "#" + member.Discriminator
}

// DiscordEmbed describes an embed for Discord in a simpler way than *discordgo.MessageEmbed.
type DiscordEmbed struct {
	Title       string
	Description string
	Fields      []DiscordEmbedField
	Color       int
}

// AddField adds a field to the embed.
func (embed *DiscordEmbed) AddField(field DiscordEmbedField) {
	embed.Fields = append(embed.Fields, field)
}

// Convert converts the data structure to the corresponding one in discordgo.
func (embed *DiscordEmbed) Convert() *discordgo.MessageEmbed {
	dgEmbed := &discordgo.MessageEmbed{
		Title:       embed.Title,
		Description: embed.Description,
		Color:       embed.Color,
		Fields:      []*discordgo.MessageEmbedField{},
	}
	for _, field := range embed.Fields {
		dgEmbed.Fields = append(dgEmbed.Fields, field.Convert())
	}
	return dgEmbed
}

// DiscordEmbedField is an easy to use description of a field for Discord's embeds.
type DiscordEmbedField struct {
	Name   string
	Value  string
	Inline bool
}

// Convert converts the data structure to the corresponding one in discordgo.
func (field DiscordEmbedField) Convert() *discordgo.MessageEmbedField {
	return &discordgo.MessageEmbedField{
		Name:   field.Name,
		Value:  field.Value,
		Inline: field.Inline,
	}
}

// DiscordCommand describes a command for DiscordBot.
type DiscordCommand struct {
	Command    string
	Aliases    []string
	Callback   func(DiscordMessage) error
	Privileged bool
	HasArgs    bool
}

// Match returns whether the message's content with the set prefix matches a registered command,
// and returns the content with the command.
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

// SingleMatch tests whether a command with a prefix matches a message's content.
// If it does, it also returns the content without the command.
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

// DiscordWelcomeData describes the data useful to welcome a new member on a server.
// To be used with a template.
type DiscordWelcomeData struct {
	BotID      string
	ChannelsID DiscordBotChannelsID
	Member     DiscordMember
}

// DiscordBot is a component that interacts with Discord.
type DiscordBot struct {
	sync.Mutex

	// dependencies
	addUser AddRedditUser
	client  *discordgo.Session
	ctx     context.Context
	logger  LevelLogger
	storage DiscordBotStorage

	// state information
	ID string

	// configuration
	adminID        string
	channelsID     DiscordBotChannelsID
	guildID        string
	hidePrefix     string
	noLog          string
	prefix         string
	privilegedRole string
	timezone       *time.Location
	welcome        *template.Template

	// miscellaneous
	commands   []DiscordCommand
	done       chan error
	redditLink *regexp.Regexp
	mention    *regexp.Regexp
}

// NewDiscordBot returns a new DiscordBot.
func NewDiscordBot(storage DiscordBotStorage, logger LevelLogger, addUser AddRedditUser, conf DiscordBotConf) (*DiscordBot, error) {
	discordgo.Logger = func(msgL, caller int, format string, dgArgs ...interface{}) {
		args := []interface{}{msgL, caller}
		args = append(args, dgArgs...)
		logger.Debugf("discordgo library (log level %d, goroutine %d): "+format, args...)
	}

	session, err := discordgo.New("Bot " + conf.Token)
	if err != nil {
		return nil, err
	}

	welcome, err := template.New("DiscordWelcome").Parse(conf.Welcome) // works even if empty
	if err != nil {
		return nil, err
	}

	bot := &DiscordBot{
		addUser: addUser,
		client:  session,
		logger:  logger,
		storage: storage,

		channelsID:     conf.DiscordBotChannelsID,
		hidePrefix:     conf.HidePrefix,
		noLog:          conf.Prefix + "nolog",
		prefix:         conf.Prefix,
		privilegedRole: conf.PrivilegedRole,
		timezone:       conf.Timezone.Value,
		welcome:        welcome,

		done:       make(chan error),
		redditLink: regexp.MustCompile(`(?s:.*reddit\.com/r/\w+/comments/.*)`),
		mention:    regexp.MustCompile(`<@([0-9]{1,21})>`),
	}

	bot.commands = bot.getCommandsDescriptors()

	if conf.Welcome != "" && conf.General != "" {
		session.AddHandler(func(s *discordgo.Session, event *discordgo.GuildMemberAdd) {
			bot.welcomeNewMember(event.Member)
		})
	}

	session.AddHandler(func(s *discordgo.Session, msg *discordgo.MessageCreate) { bot.onMessage(msg) })
	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) { bot.onReady(r) })

	return bot, nil
}

// Run runs and blocks until the bot stops.
func (bot *DiscordBot) Run(ctx context.Context) error {
	bot.ctx = ctx

	go func() {
		if err := bot.client.Open(); err != nil {
			if !IsCancellation(err) {
				err = fmt.Errorf("discord bot: %v", err)
			}
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

func (bot *DiscordBot) channelEmbedSend(channelID string, embed *DiscordEmbed) error {
	embed.Color = bot.myColor(channelID)
	_, err := bot.client.ChannelMessageSendEmbed(channelID, embed.Convert())
	return err
}

func (bot *DiscordBot) channelErrorSend(channelID, userID, content string) error {
	reply := fmt.Sprintf("<@%s>%s%s%s", userID, EmojiCrossMark, content, EmojiCrossMark)
	msg, err := bot.client.ChannelMessageSend(channelID, reply)
	if err != nil {
		return err
	}
	go func() {
		time.Sleep(discordMessageDeletionWait)
		if err := bot.client.ChannelMessageDelete(channelID, msg.ID); err != nil {
			bot.logger.Errorf("error when deleting discord error message %q: %v", content, err)
		}
	}()
	return nil
}

func (bot *DiscordBot) myColor(channelID string) int {
	if bot.client.StateEnabled {
		return bot.client.State.UserColor(bot.ID, channelID)
	}
	return DiscordDefaultRoleColor
}

// this is executed on each (re)-connection to Discord
func (bot *DiscordBot) onReady(r *discordgo.Ready) {
	bot.logger.Debug("(re-)connected to discord, checking settings")

	// set status
	if err := bot.client.UpdateStatus(0, "Downvote Counter"); err != nil {
		bot.fatal(fmt.Errorf("couldn't set status on discord: %v", err))
		return
	}

	// guild-related information and checks
	if nb := len(r.Guilds); nb != 1 {
		bot.fatal(fmt.Errorf("the bot needs to be in one and only one discord server (found in %d server(s))", nb))
		return
	}

	// The data structure representing guilds only has their ID set at this point.
	bot.guildID = r.Guilds[0].ID

	guild, err := bot.client.Guild(bot.guildID)
	if err != nil {
		bot.fatal(fmt.Errorf("error when getting information about guild %q: %v", bot.guildID, err))
	}

	bot.adminID = guild.OwnerID

	if bot.privilegedRole == "" {
		bot.logger.Info("no privileged discord role has been set, only the server's owner can use privileged commands")
	} else if !rolesHaveRoleID(guild.Roles, bot.privilegedRole) {
		bot.fatal(fmt.Errorf("the discord server doesn't have a role with ID %s", bot.privilegedRole))
		return
	}

	// check the channels
	channels := reflect.ValueOf(bot.channelsID)
	for i := 0; i < channels.NumField(); i++ {
		channelID := channels.Field(i).String()
		name := channels.Type().Field(i).Name

		if channelID == "" {
			continue
		}

		if _, err := bot.client.Channel(channelID); err != nil {
			bot.fatal(fmt.Errorf("discord channel %s: %v", name, err))
			return
		}
	}

	// other

	bot.ID = r.User.ID
}

func (bot *DiscordBot) welcomeNewMember(member *discordgo.Member) {
	var msg strings.Builder
	data := DiscordWelcomeData{
		BotID:      bot.ID,
		ChannelsID: bot.channelsID,
		Member: DiscordMember{
			ID:            member.User.ID,
			Name:          member.User.Username,
			Discriminator: member.User.Discriminator,
		},
	}
	bot.logger.Debugf("welcoming discord user %s", data.Member.FQN())
	if err := bot.welcome.Execute(&msg, data); err != nil {
		bot.fatal(err)
	}
	if err := bot.channelMessageSend(bot.channelsID.General, msg.String()); err != nil {
		bot.logger.Error(err)
	}
}

func (bot *DiscordBot) onMessage(dgMsg *discordgo.MessageCreate) {
	var err error

	if dgMsg.Author.ID == bot.ID {
		return
	}

	isDm, err := bot.isDMChannel(dgMsg.ChannelID)
	if err != nil {
		bot.logger.Error(err)
	}

	msg := NewDiscordMessage(dgMsg)
	msg.IsDM = isDm

	if bot.isLoggableRedditLink(msg) {
		err = bot.processRedditLink(msg)
	} else {
		err = bot.command(msg)
	}

	if err != nil {
		bot.logger.Error(err)
	}
}

// SignalSuspensions signals on discord the suspended or deleted User sent on the given channel.
// It needs to be launched independently of the bot.
func (bot *DiscordBot) SignalSuspensions(suspensions <-chan User) {
	for user := range suspensions {
		state := "suspended"
		if user.NotFound {
			state = "deleted"
		}
		msg := fmt.Sprintf("RIP /u/%s %s (%s)", user.Name, EmojiPrayingHands, state)
		if err := bot.channelMessageSend(bot.channelsID.General, msg); err != nil {
			bot.logger.Errorf("error when signaling a suspension or deletion: %v", err)
		}
	}
}

// SignalUnsuspensions signals on discord as unsuspensions any User sent on the given channel.
// It needs to be launched independently of the bot.
func (bot *DiscordBot) SignalUnsuspensions(ch <-chan User) {
	for user := range ch {
		msg := fmt.Sprintf("%s /u/%s has been unsuspended! %s", EmojiRainbow, user.Name, EmojiRainbow)
		if err := bot.channelMessageSend(bot.channelsID.General, msg); err != nil {
			bot.logger.Errorf("error when signaling an unsuspensions: %v", err)
		}
	}
}

// SignalHighScores signals on discord as high scores any Comment sent on the given channel.
// It needs to be launched independently of the bot.
func (bot *DiscordBot) SignalHighScores(ch <-chan Comment) {
	for comment := range ch {
		link := "https://www.reddit.com" + comment.Permalink
		tmpl := "a comment by /u/%s has reached %d: %s"
		msg := fmt.Sprintf(tmpl, comment.Author, comment.Score, link)
		if err := bot.channelMessageSend(bot.channelsID.HighScores, msg); err != nil {
			bot.logger.Errorf("error when signaling high-score: %v", err)
		}
	}
}

func (bot *DiscordBot) matchCommand(msg DiscordMessage) (DiscordCommand, DiscordMessage) {
	for _, cmd := range bot.commands {

		if matches, contentRest := cmd.Match(bot.prefix, msg.Content); matches {

			// if the command was issued by someone who is not the admin, check role
			if cmd.Privileged && msg.Author.ID != bot.adminID {
				if bot.privilegedRole == "" {
					continue
				}

				member, err := bot.client.GuildMember(bot.guildID, msg.Author.ID)
				if err != nil {
					bot.logger.Error(err)
					continue
				}
				if !SliceHasString(member.Roles, bot.privilegedRole) {
					if err := bot.channelErrorSend(msg.ChannelID, msg.Author.ID, "You are not allowed to use this command."); err != nil {
						bot.logger.Error(err)
					}
					continue
				}
			}

			msg.Content = contentRest
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
	bot.logger.Debugf("matched command %q, args %v, from user %s", cmd.Command, msg.Args, msg.Author.FQN())

	if err := bot.client.ChannelTyping(msg.ChannelID); err != nil {
		return err
	}

	err := cmd.Callback(msg)

	if err == nil && !msg.IsDM {
		time.Sleep(discordMessageDeletionWait)
		err = bot.client.ChannelMessageDelete(msg.ChannelID, msg.ID)
	}

	return err
}

func (bot *DiscordBot) isLoggableRedditLink(msg DiscordMessage) bool {
	return (msg.ChannelID == bot.channelsID.General && // won't be true if General is not set (ie left empty)
		bot.redditLink.MatchString(msg.Content) &&
		!strings.Contains(strings.ToLower(msg.Content), bot.noLog))
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
	nbReactions := len(linkReactions)
	randIndex := rand.Int31n(int32(nbReactions))
	reaction := linkReactions[randIndex]
	return bot.client.MessageReactionAdd(msg.ChannelID, msg.ID, reaction)
}

func (bot *DiscordBot) getCommandsDescriptors() []DiscordCommand {
	return []DiscordCommand{{
		Command:  "karma",
		Callback: bot.karma,
		HasArgs:  true,
	}, {
		Command:  "karma",
		Callback: bot.simpleError("Type \"%skarma reddit-username\" to get the karma stats of \"reddit-username\".", bot.prefix),
		HasArgs:  false,
	}, {
		Command:    "version",
		Callback:   bot.simpleReply(Version.String()),
		Privileged: true,
	}, {
		Command:  "register",
		Callback: bot.register,
		HasArgs:  true,
	}, {
		Command:    "unregister",
		Callback:   bot.editUsers("unregister", bot.storage.DelUser),
		HasArgs:    true,
		Privileged: true,
	}, {
		Command:    "purge",
		Callback:   bot.editUsers("purge", bot.storage.PurgeUser),
		HasArgs:    true,
		Privileged: true,
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
		Callback: bot.simpleReply("More like N0000 1 cares %s This shitpost is horrible %s",
			EmojiFire, strings.Repeat(EmojiThumbDown, 3)),
	}, {
		Command:  "separator",
		Aliases:  []string{"sep", "="},
		Callback: bot.simpleReply("══════════════════"),
	}, {
		Command:    "ban",
		Callback:   bot.ban,
		HasArgs:    true,
		Privileged: true,
	}}
}

func (bot *DiscordBot) simpleReply(str string, args ...interface{}) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		return bot.channelMessageSend(msg.ChannelID, fmt.Sprintf(str, args...))
	}
}

func (bot *DiscordBot) simpleError(str string, args ...interface{}) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		return bot.channelErrorSend(msg.ChannelID, msg.Author.ID, fmt.Sprintf(str, args...))
	}
}

func (bot *DiscordBot) register(msg DiscordMessage) error {
	names := msg.Args
	bot.logger.Infof("%s wants to register %v", msg.Author.FQN(), names)

	if bot.addUser == nil {
		return bot.channelErrorSend(msg.ChannelID, msg.Author.ID, "registration service is unavailable")
	}

	status := &DiscordEmbed{
		Title:       "Registration",
		Description: fmt.Sprintf("request from <@%s>", msg.Author.ID),
	}

	for _, name := range names {
		if err := bot.client.ChannelTyping(msg.ChannelID); err != nil {
			return err
		}

		hidden := strings.HasPrefix(name, bot.hidePrefix)
		name = TrimUsername(strings.TrimPrefix(name, bot.hidePrefix))

		reply := bot.addUser(bot.ctx, name, hidden, false)

		if IsCancellation(reply.Error) {
			continue
		} else if reply.Error != nil {
			status.AddField(DiscordEmbedField{Name: reply.User.Name, Value: fmt.Sprintf("%s %s", EmojiCrossMark, reply.Error)})
		} else if !reply.Exists {
			status.AddField(DiscordEmbedField{Name: reply.User.Name, Value: EmojiCrossMark + " not found"})
		} else {
			status.AddField(DiscordEmbedField{Name: reply.User.Name, Value: EmojiCheckMark})
		}
	}

	return bot.channelEmbedSend(msg.ChannelID, status)
}

func (bot *DiscordBot) editUsers(actionName string, action func(context.Context, string) error) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		names := msg.Args
		bot.logger.Infof("%s wants to %s %v", msg.Author.FQN(), actionName, names)

		status := &DiscordEmbed{
			Title:       strings.Title(actionName),
			Description: fmt.Sprintf("request from <@%s>", msg.Author.ID),
		}

		for _, name := range names {
			name = TrimUsername(name)
			if err := action(bot.ctx, name); err != nil {
				status.AddField(DiscordEmbedField{Name: name, Value: fmt.Sprintf("%s %s", EmojiCrossMark, err)})
			} else {
				status.AddField(DiscordEmbedField{Name: name, Value: EmojiCheckMark})
			}
		}

		return bot.channelEmbedSend(msg.ChannelID, status)
	}
}

func (bot *DiscordBot) userInfo(msg DiscordMessage) error {
	username := TrimUsername(msg.Content)

	query := bot.storage.GetUser(bot.ctx, username)

	if !query.Exists {
		response := fmt.Sprintf("user %q not found in the database.", username)
		return bot.channelErrorSend(msg.ChannelID, msg.Author.ID, response)
	}

	user := query.User

	embed := &DiscordEmbed{
		Title: "Information about /u/" + user.Name,
		Color: bot.myColor(msg.ChannelID),
		Fields: []DiscordEmbedField{{
			Name:   "Created",
			Value:  user.Created.In(bot.timezone).Format(time.RFC850),
			Inline: true,
		}, {
			Name:   "Added",
			Value:  user.Added.In(bot.timezone).Format(time.RFC850),
			Inline: true,
		}, {
			Name:   "Hidden",
			Value:  fmt.Sprintf("%t", user.Hidden),
			Inline: true,
		}, {
			Name:   "Suspended",
			Value:  fmt.Sprintf("%t", user.Suspended),
			Inline: true,
		}, {
			Name:   "Deleted",
			Value:  fmt.Sprintf("%t", user.NotFound),
			Inline: true,
		}, {
			Name:   "Active",
			Value:  fmt.Sprintf("%t", !user.Inactive),
			Inline: true,
		}},
	}

	if !user.LastScan.IsZero() {
		embed.AddField(DiscordEmbedField{
			Name:   "Last scan",
			Value:  user.LastScan.In(bot.timezone).Format(time.RFC850),
			Inline: true,
		})
	}

	return bot.channelEmbedSend(msg.ChannelID, embed)
}

func (bot *DiscordBot) karma(msg DiscordMessage) error {
	if len(msg.Args) > 1 {
		return bot.channelErrorSend(msg.ChannelID, msg.Author.ID, "Only one username at a time is accepted.")
	}

	username := TrimUsername(msg.Args[0])

	userQuery := bot.storage.GetUser(bot.ctx, username)
	if userQuery.Error != nil {
		return userQuery.Error
	} else if !userQuery.Exists {
		reply := fmt.Sprintf("user %s not found.", username)
		return bot.channelErrorSend(msg.ChannelID, msg.Author.ID, reply)
	}

	user := userQuery.User

	total, negative, err := bot.storage.GetKarma(bot.ctx, user.Name)
	if err != nil {
		return err
	}

	embed := &DiscordEmbed{
		Title: "Karma for /u/" + user.Name,
		Fields: []DiscordEmbedField{
			{Name: "Positive", Value: fmt.Sprintf("%d", total-negative), Inline: true},
			{Name: "Negative", Value: fmt.Sprintf("%d", negative), Inline: true},
			{Name: "Total", Value: fmt.Sprintf("%d", total), Inline: true},
		},
	}
	if user.New {
		embed.Description = EmojiWarning + " _this user hasn't been fully scanned yet._"
	}
	return bot.channelEmbedSend(msg.ChannelID, embed)
}

func (bot *DiscordBot) ban(msg DiscordMessage) error {
	if len(msg.Args) == 0 {
		if err := bot.channelErrorSend(msg.ChannelID, msg.Author.ID, "A mention of the user to ban is required."); err != nil {
			return err
		}
	}

	matches := bot.mention.FindStringSubmatch(msg.Args[0])
	if len(matches) == 0 {
		if err := bot.channelErrorSend(msg.ChannelID, msg.Author.ID, "Invalid user mention."); err != nil {
			return err
		}
	}

	id := matches[1]

	reason := strings.Join(msg.Args[1:], " ")
	if reason == "" {
		if err := bot.client.GuildBanCreate(bot.guildID, id, 0); err != nil {
			return err
		}
	} else {
		if err := bot.client.GuildBanCreateWithReason(bot.guildID, id, reason, 0); err != nil {
			return err
		}
	}

	return nil
}

// TrimUsername trims reddit user names so that the application is liberal in what it accepts.
func TrimUsername(username string) string {
	return strings.TrimPrefix(strings.TrimPrefix(username, "/u/"), "u/")
}

func rolesHaveRoleID(roles []*discordgo.Role, roleID string) bool {
	for _, role := range roles {
		if role.ID == roleID {
			return true
		}
	}
	return false
}
