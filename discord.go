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

// discordgo's data structures aren't well adapted to our needs,
// and typing "*discordgo.<DataStructure>" all the time gets tiring.
type DiscordMessage struct {
	Args      []string
	Author    DiscordMember
	Content   string
	ChannelID string
	IsDM      bool
	ID        string
}

func NewDiscordMessage(dg_msg *discordgo.MessageCreate) DiscordMessage {
	return DiscordMessage{
		ID:        dg_msg.ID,
		Content:   dg_msg.Content,
		ChannelID: dg_msg.ChannelID,
		Author: DiscordMember{
			ID:            dg_msg.Author.ID,
			Name:          dg_msg.Author.Username,
			Discriminator: dg_msg.Author.Discriminator,
		},
	}
}

// This usefully subsumes discordgo.Member and discordgo.User
type DiscordMember struct {
	ID            string
	Name          string
	Discriminator string
}

func (member DiscordMember) FQN() string {
	return member.Name + "#" + member.Discriminator
}

type DiscordEmbed struct {
	Title       string
	Description string
	Fields      []DiscordEmbedField
	Color       int
}

func (embed *DiscordEmbed) AddField(field DiscordEmbedField) {
	embed.Fields = append(embed.Fields, field)
}

func (embed *DiscordEmbed) Convert() *discordgo.MessageEmbed {
	dg_embed := &discordgo.MessageEmbed{
		Title:       embed.Title,
		Description: embed.Description,
		Color:       embed.Color,
		Fields:      []*discordgo.MessageEmbedField{},
	}
	for _, field := range embed.Fields {
		dg_embed.Fields = append(dg_embed.Fields, field.Convert())
	}
	return dg_embed
}

type DiscordEmbedField struct {
	Name   string
	Value  string
	Inline bool
}

func (field DiscordEmbedField) Convert() *discordgo.MessageEmbedField {
	return &discordgo.MessageEmbedField{
		Name:   field.Name,
		Value:  field.Value,
		Inline: field.Inline,
	}
}

// This is used in DiscordBot.getCommandsDescriptors.
type DiscordCommand struct {
	Command    string
	Aliases    []string
	Callback   func(DiscordMessage) error
	Privileged bool
	HasArgs    bool
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
	BotID      string
	ChannelsID DiscordBotChannelsID
	Member     DiscordMember
}

// Component
type DiscordBot struct {
	sync.Mutex

	// dependencies
	client  *discordgo.Session
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
	addUser    chan UserQuery
	commands   []DiscordCommand
	done       chan error
	redditLink *regexp.Regexp
	mention    *regexp.Regexp
}

func NewDiscordBot(storage DiscordBotStorage, logger LevelLogger, conf DiscordBotConf) (*DiscordBot, error) {
	discordgo.Logger = func(msgL, caller int, format string, dg_args ...interface{}) {
		args := []interface{}{msgL, caller}
		args = append(args, dg_args...)
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

func (bot *DiscordBot) Run(ctx context.Context) error {
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

func (bot *DiscordBot) OpenAddUser() chan UserQuery {
	bot.Lock()
	defer bot.Unlock()
	if bot.addUser == nil {
		bot.addUser = make(chan UserQuery, DefaultChannelSize)
	}
	return bot.addUser
}

func (bot *DiscordBot) CloseAddUser() {
	bot.Lock()
	defer bot.Unlock()
	if bot.addUser != nil {
		close(bot.addUser)
		bot.addUser = nil
	}
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
	}

	guild := r.Guilds[0]

	bot.guildID = guild.ID

	bot.adminID = guild.OwnerID

	roles, err := bot.client.GuildRoles(guild.ID)
	if err != nil {
		bot.fatal(fmt.Errorf("error when getting roles on the discord server: %v", err))
	}

	if bot.privilegedRole == "" {
		bot.logger.Info("no privileged discord role has been set, only the server's owner can use privileged commands")
	} else if !rolesHaveRoleID(roles, bot.privilegedRole) {
		bot.fatal(fmt.Errorf("the discord server doesn't have a role with ID %s", bot.privilegedRole))
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

func (bot *DiscordBot) onMessage(dg_msg *discordgo.MessageCreate) {
	var err error

	if dg_msg.Author.ID == bot.ID {
		return
	}

	is_dm, err := bot.isDMChannel(dg_msg.ChannelID)
	if err != nil {
		bot.logger.Error(err)
	}

	msg := NewDiscordMessage(dg_msg)
	msg.IsDM = is_dm

	if bot.isLoggableRedditLink(msg) {
		err = bot.processRedditLink(msg)
	} else {
		err = bot.command(msg)
	}

	if err != nil {
		bot.logger.Error(err)
	}
}

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

func (bot *DiscordBot) SignalUnsuspensions(ch <-chan User) {
	for user := range ch {
		msg := fmt.Sprintf("%s /u/%s has been unsuspended! %s", EmojiRainbow, user.Name, EmojiRainbow)
		if err := bot.channelMessageSend(bot.channelsID.General, msg); err != nil {
			bot.logger.Errorf("error when signaling an unsuspensions: %v", err)
		}
	}
}

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

		if matches, content_rest := cmd.Match(bot.prefix, msg.Content); matches {

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
		Callback: bot.simpleReply(fmt.Sprintf(
			"More like N0000 1 cares %s This shitpost is horrible %s",
			EmojiFire, strings.Repeat(EmojiThumbDown, 3))),
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

func (bot *DiscordBot) simpleReply(reply string) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		return bot.channelMessageSend(msg.ChannelID, reply)
	}
}

func (bot *DiscordBot) register(msg DiscordMessage) error {
	names := msg.Args
	bot.logger.Infof("%s wants to register %v", msg.Author.FQN(), names)

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

		bot.Lock()
		if bot.addUser == nil {
			bot.Unlock()
			status.AddField(DiscordEmbedField{Name: name, Value: EmojiCrossMark + " registration service unavailable"})
			continue
		}
		bot.addUser <- UserQuery{User: User{Name: name, Hidden: hidden}}
		reply := <-bot.addUser
		bot.Unlock()

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

func (bot *DiscordBot) editUsers(action_name string, action func(string) error) func(DiscordMessage) error {
	return func(msg DiscordMessage) error {
		names := msg.Args
		bot.logger.Infof("%s wants to %s %v", msg.Author.FQN(), action_name, names)

		status := &DiscordEmbed{
			Title:       strings.Title(action_name),
			Description: fmt.Sprintf("request from <@%s>", msg.Author.ID),
		}

		for _, name := range names {
			name = TrimUsername(name)
			if err := action(name); err != nil {
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

	query := bot.storage.GetUser(username)

	if !query.Exists {
		response := fmt.Sprintf("user '%s' not found in the database.", username)
		return bot.channelErrorSend(msg.ChannelID, msg.Author.ID, response)
	}

	user := query.User

	embed := &DiscordEmbed{
		Title: "Information about /u/" + user.Name,
		Color: bot.myColor(msg.ChannelID),
		Fields: []DiscordEmbedField{{
			Name:   "Created",
			Value:  user.CreatedTime().In(bot.timezone).Format(time.RFC850),
			Inline: true,
		}, {
			Name:   "Added",
			Value:  user.AddedTime().In(bot.timezone).Format(time.RFC850),
			Inline: true,
		}, {
			Name:   "Hidden from reports",
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

	if user.LastScan > 0 {
		embed.AddField(DiscordEmbedField{
			Name:   "Last scan",
			Value:  user.LastScanTime().In(bot.timezone).Format(time.RFC850),
			Inline: true,
		})
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
		reply := fmt.Sprintf("user %s not found.", username)
		return bot.channelErrorSend(msg.ChannelID, msg.Author.ID, reply)
	}

	embed := &DiscordEmbed{Title: "Karma for /u/" + res.User.Name}

	var positive int64
	var negative int64
	var err error

	positive, err = bot.storage.GetPositiveKarma(username)
	if err == ErrNoComment {
		embed.AddField(DiscordEmbedField{Name: "Positive", Value: "N/A", Inline: true})
	} else if err != nil {
		return err
	} else {
		embed.AddField(DiscordEmbedField{Name: "Positive", Value: fmt.Sprintf("%d", positive), Inline: true})
	}

	negative, err = bot.storage.GetNegativeKarma(username)
	if err == ErrNoComment {
		embed.AddField(DiscordEmbedField{Name: "Negative", Value: "N/A", Inline: true})
	} else if err != nil {
		return err
	} else {
		embed.AddField(DiscordEmbedField{Name: "Negative", Value: fmt.Sprintf("%d", negative), Inline: true})
	}

	embed.AddField(DiscordEmbedField{Name: "Total", Value: fmt.Sprintf("%d", positive+negative), Inline: true})

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

func TrimUsername(username string) string {
	return strings.TrimPrefix(strings.TrimPrefix(username, "/u/"), "u/")
}

func rolesHaveRoleID(roles []*discordgo.Role, role_id string) bool {
	for _, role := range roles {
		if role.ID == role_id {
			return true
		}
	}
	return false
}
