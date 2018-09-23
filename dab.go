package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"strings"
)

type DownArrowsBot struct {
	LoggerOpts int
	Logger     *log.Logger
	LogOut     io.Writer
	FlagSet    *flag.FlagSet

	Conf Configuration

	RuntimeConf struct {
		UserAdd    bool
		Report     bool
		InitDB     bool
		Compendium bool
		Reddit     bool
		Discord    bool
		Web        bool
	}

	Components struct {
		Enabled []string
		Storage *Storage
		Reddit  *RedditBot
		Discord *DiscordBot
		Report  ReportFactory
		Web     *WebServer
	}
}

func NewDownArrowsBot(log_out io.Writer, logger_opts int) *DownArrowsBot {
	return &DownArrowsBot{
		LoggerOpts: logger_opts,
		Logger:     log.New(log_out, "", logger_opts),
		LogOut:     log_out,
		FlagSet:    flag.NewFlagSet("DownArrowsBot", flag.ExitOnError),
	}
}

func (dab *DownArrowsBot) Launch(args []string) bool {
	var daemon bool

	dab.init(args)

	dab.initStorage()

	if dab.RuntimeConf.InitDB {
		return false
	}

	dab.Components.Report = NewReportFactory(dab.Components.Storage, dab.Conf.Report)
	if dab.RuntimeConf.Report {
		dab.report()
		return false
	}

	dab.checkRedditConf()
	if dab.RuntimeConf.Reddit {
		dab.initReddit()
	}

	if dab.RuntimeConf.UserAdd {
		if !dab.RuntimeConf.Reddit {
			dab.Logger.Fatal("reddit bot must be running to register users")
		}
		dab.userAdd()
		return false
	}

	if dab.RuntimeConf.Reddit {
		dab.launchReddit()
		daemon = true
	}

	dab.checkDiscordConf()
	if dab.RuntimeConf.Discord {
		dab.launchDiscord()
		daemon = true
	}

	if dab.RuntimeConf.Reddit && dab.RuntimeConf.Discord {
		dab.connectRedditAndDiscord()
	}

	dab.checkWebConf()
	if dab.RuntimeConf.Web {
		dab.launchWeb()
		daemon = true
	}

	dab.Logger.Print("launched the following components: ", strings.Join(dab.Components.Enabled, ", "))

	return daemon
}

func (dab *DownArrowsBot) Close() {
	c := dab.Components
	if c.Discord != nil {
		if err := c.Discord.Close(); err != nil {
			dab.Logger.Print(err)
		}
	}
	if c.Web != nil {
		c.Web.Close()
	}
	if c.Storage != nil {
		c.Storage.Close()
	}
	dab.Logger.Print("DownArrowsBot stopped")
}

func (dab *DownArrowsBot) init(args []string) {
	path := dab.FlagSet.String("config", "./dab.conf.json", "Path to the configuration file.")
	dab.FlagSet.BoolVar(&dab.RuntimeConf.InitDB, "initdb", false, "Initialize the database and exit.")
	dab.FlagSet.BoolVar(&dab.RuntimeConf.Report, "report", false, "Print the report for the last week and exit.")
	dab.FlagSet.BoolVar(&dab.RuntimeConf.UserAdd, "useradd", false, "Add one or multiple usernames to be tracked and exit.")
	dab.FlagSet.BoolVar(&dab.RuntimeConf.Compendium, "compendium", false, "Start the reddit bot with an update from DVT's compendium.")
	dab.FlagSet.Parse(args)

	if !dab.RuntimeConf.UserAdd && dab.FlagSet.NArg() > 0 {
		dab.Logger.Fatal("No argument besides usernames when adding users is accepted")
	}

	var err error
	dab.Conf, err = NewConfiguration(*path)
	if err != nil {
		dab.Logger.Fatal(err)
	}
}

func (dab *DownArrowsBot) checkRedditConf() {
	if dab.Conf.Reddit.Username == "" || dab.Conf.Reddit.Secret == "" || dab.Conf.Reddit.Id == "" ||
		dab.Conf.Reddit.Password == "" || dab.Conf.Reddit.UserAgent == "" {
		fields := "id, secret, username, password, user_agent"
		msg := "Disabling reddit bot; at least one of the required fields of 'reddit' in the configuration file is empty"
		dab.Logger.Print(msg, ": ", fields)
	} else {
		dab.RuntimeConf.Reddit = true
		dab.Components.Enabled = append(dab.Components.Enabled, "reddit")
	}
}

func (dab *DownArrowsBot) checkDiscordConf() {
	if dab.Conf.Discord.DiscordBotConf.HidePrefix == "" {
		dab.Conf.Discord.DiscordBotConf.HidePrefix = dab.Conf.HidePrefix
	}

	dab.RuntimeConf.Discord = dab.Conf.Discord.Token != ""
	if dab.RuntimeConf.Discord {
		dab.Components.Enabled = append(dab.Components.Enabled, "discord")
	} else {
		dab.Logger.Print("disabling discord bot; empty 'token' field in 'discord' section of the configuration file")
	}
}

func (dab *DownArrowsBot) initStorage() {
	dab.Logger.Print("using database ", dab.Conf.Database.Path)
	dab.Components.Storage = NewStorage(dab.Conf.Database)
	dab.Components.Enabled = append(dab.Components.Enabled, "storage")
}

func (dab *DownArrowsBot) initReddit() {
	dab.Logger.Print("attempting to log reddit bot in")
	scanner, err := NewScanner(dab.Conf.Reddit.RedditAuth, dab.Conf.Reddit.UserAgent)
	if err != nil {
		dab.Logger.Fatal(err)
	}
	dab.Logger.Print("reddit bot successfully logged in")
	bot_logger := log.New(dab.LogOut, "", dab.LoggerOpts)
	dab.Components.Reddit = NewRedditBot(scanner, dab.Components.Storage, bot_logger, dab.Conf.Reddit.RedditBotConf)
}

func (dab *DownArrowsBot) initReport() {
	dab.Components.Report = NewReportFactory(dab.Components.Storage, dab.Conf.Report)
}

func (dab *DownArrowsBot) report() {
	dab.Logger.Print("printing report for last week")
	report := dab.Components.Report.ReportWeek(dab.Components.Report.LastWeekCoordinates())
	if report.Len() == 0 {
		dab.Logger.Fatal("empty report")
	}
	fmt.Println(report)
}

func (dab *DownArrowsBot) userAdd() {
	usernames := dab.FlagSet.Args()
	for _, username := range usernames {
		hidden := strings.HasPrefix(username, dab.Conf.HidePrefix)
		username = strings.TrimPrefix(username, dab.Conf.HidePrefix)
		if res := dab.Components.Reddit.AddUser(username, hidden, true); res.Error != nil && !res.Exists {
			dab.Logger.Fatal(res.Error)
		}
	}
}

func (dab *DownArrowsBot) launchReddit() {
	if dab.RuntimeConf.Compendium {
		if err := dab.Components.Reddit.UpdateUsersFromCompendium(); err != nil {
			dab.Logger.Print(err)
		}
	}

	go dab.Components.Reddit.Run()

	go dab.Components.Reddit.AutoCompendiumUpdate(dab.Conf.Reddit.CompendiumUpdateInterval.Value)
}

func (dab *DownArrowsBot) launchDiscord() {
	var err error

	dab.Logger.Print("attempting to connect discord bot")
	bot_logger := log.New(dab.LogOut, "", dab.LoggerOpts)

	dab.Components.Discord, err = NewDiscordBot(dab.Components.Storage, bot_logger, dab.Conf.Discord.DiscordBotConf)
	if err != nil {
		dab.Logger.Fatal(err)
	}
	if err := dab.Components.Discord.Run(); err != nil {
		dab.Logger.Fatal(err)
	}
	dab.Logger.Print("discord bot connected")
}

func (dab *DownArrowsBot) connectRedditAndDiscord() {
	dab.Logger.Print("connecting the discord bot and the reddit bot together")
	go dab.Components.Reddit.AddUserServer(dab.Components.Discord.AddUser)

	reddit_evts := make(chan Comment)
	go dab.Components.Discord.RedditEvents(reddit_evts)
	go dab.Components.Reddit.StreamSub("DownvoteTrolling", reddit_evts, dab.Conf.Reddit.DVTInterval.Value)

	suspensions := dab.Components.Reddit.Suspensions()
	go dab.Components.Discord.SignalSuspensions(suspensions)

	unsuspensions := dab.Components.Reddit.CheckUnsuspended(dab.Conf.Reddit.UnsuspensionInterval.Value)
	go dab.Components.Discord.SignalUnsuspensions(unsuspensions)

	if dab.Conf.Discord.HighScores != "" {
		highscores := dab.Components.Reddit.StartHighScoresFeed(dab.Conf.Discord.HighScoreThreshold)
		go dab.Components.Discord.SignalHighScores(highscores)
	}
	dab.Logger.Print("discord bot and reddit bot connected")
}

func (dab *DownArrowsBot) checkWebConf() {
	if dab.Conf.Web.Listen != "" {
		dab.RuntimeConf.Web = true
		dab.Components.Enabled = append(dab.Components.Enabled, "web server")
	} else {
		dab.Logger.Print("disabling web server; empty 'listen' field in 'web' section of the configuration file")
	}
}

func (dab *DownArrowsBot) launchWeb() {
	dab.Logger.Print("lauching the web server")
	dab.Components.Web = NewWebServer(dab.Conf.Web.Listen, dab.Components.Report)
	go func() {
		err := dab.Components.Web.Run()
		if err != nil {
			dab.Logger.Print(err)
		}
	}()
}
