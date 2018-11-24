package main

import (
	"context"
	"flag"
	"io"
	"log"
	"strings"
	"sync"
	"text/template"
	"time"
)

const Version = "0.243"

type DownArrowsBot struct {
	flagSet    *flag.FlagSet
	Logger     *log.Logger
	loggerOpts int
	logOut     io.Writer
	stdOut     io.Writer

	cancel       context.CancelFunc
	waitShutdown *sync.WaitGroup

	conf   Configuration
	Daemon bool

	runtimeConf struct {
		Compendium bool
		Discord    bool
		InitDB     bool
		Launched   bool
		Reddit     bool
		Report     bool
		Timezone   *time.Location
		UserAdd    bool
		Web        bool
	}

	components struct {
		Enabled       []string
		Discord       *DiscordBot
		RedditScanner *RedditScanner
		RedditUsers   *RedditUsers
		RedditSubs    *RedditSubs
		Report        ReportFactory
		Storage       *Storage
		Web           *WebServer
	}
}

func NewDownArrowsBot(log_out io.Writer, logger_opts int, output io.Writer) *DownArrowsBot {
	return &DownArrowsBot{
		flagSet:      flag.NewFlagSet("DownArrowsBot", flag.ExitOnError),
		Logger:       log.New(log_out, "", logger_opts),
		loggerOpts:   logger_opts,
		logOut:       log_out,
		stdOut:       output,
		waitShutdown: &sync.WaitGroup{},
	}
}

func (dab *DownArrowsBot) Launch(ctx context.Context, args []string) {
	ctx, dab.cancel = context.WithCancel(ctx)

	dab.init(args)

	dab.initStorage()

	if dab.runtimeConf.InitDB {
		return
	}

	dab.initReport()
	if dab.runtimeConf.Report {
		dab.report()
		return
	}

	dab.checkRedditConf()
	if dab.runtimeConf.Reddit {
		dab.initReddit(ctx)
	}

	if dab.runtimeConf.UserAdd {
		if !dab.runtimeConf.Reddit {
			dab.Logger.Fatal("reddit bot must be running to register users")
		}
		dab.userAdd(ctx)
		return
	}

	if dab.runtimeConf.Reddit {
		dab.launchReddit(ctx)
	}

	dab.checkDiscordConf()
	if dab.runtimeConf.Discord {
		dab.launchDiscord()
	}

	if dab.runtimeConf.Reddit && dab.runtimeConf.Discord {
		dab.connectRedditAndDiscord(ctx)
	}

	dab.checkWebConf()
	if dab.runtimeConf.Web {
		dab.launchWeb()
	}

	dab.Logger.Print("launched the following components: ", strings.Join(dab.components.Enabled, ", "))

	dab.runtimeConf.Launched = true
}

func (dab *DownArrowsBot) Close() {
	dab.cancel()
	dab.waitShutdown.Wait()

	c := dab.components

	// the reddit bot and its components already close themselves with the context's cancellation

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

func (dab *DownArrowsBot) withShutdown(cb func()) {
	dab.waitShutdown.Add(1)
	cb()
	dab.waitShutdown.Done()
}

func (dab *DownArrowsBot) init(args []string) {
	path := dab.flagSet.String("config", "./dab.conf.json", "Path to the configuration file.")
	dab.flagSet.BoolVar(&dab.runtimeConf.Compendium, "compendium", false, "Start the reddit bot with an update from DVT's compendium.")
	dab.flagSet.BoolVar(&dab.runtimeConf.InitDB, "initdb", false, "Initialize the database and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.Report, "report", false, "Print the report for the last week and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.UserAdd, "useradd", false, "Add one or multiple usernames to be tracked and exit.")
	dab.flagSet.Parse(args)

	if !dab.runtimeConf.UserAdd && dab.flagSet.NArg() > 0 {
		dab.Logger.Fatal("No argument besides usernames when adding users is accepted")
	}

	var err error
	dab.conf, err = NewConfiguration(*path)
	if err != nil {
		dab.Logger.Fatal(err)
	}
}

func (dab *DownArrowsBot) checkRedditConf() {
	if dab.conf.Reddit.Username == "" || dab.conf.Reddit.Secret == "" || dab.conf.Reddit.Id == "" ||
		dab.conf.Reddit.Password == "" || dab.conf.Reddit.UserAgent == "" {
		fields := "id, secret, username, password, user_agent"
		msg := "Disabling reddit bot; at least one of the required fields of 'reddit' in the configuration file is empty"
		dab.Logger.Print(msg, ": ", fields)
	} else {
		dab.runtimeConf.Reddit = true
		dab.components.Enabled = append(dab.components.Enabled, "reddit")
	}
}

func (dab *DownArrowsBot) checkDiscordConf() {
	if dab.conf.Discord.DiscordBotConf.HidePrefix == "" {
		dab.conf.Discord.DiscordBotConf.HidePrefix = dab.conf.HidePrefix
	}

	dab.runtimeConf.Discord = dab.conf.Discord.Token != ""
	if dab.runtimeConf.Discord {
		dab.components.Enabled = append(dab.components.Enabled, "discord")
	} else {
		dab.Logger.Print("disabling discord bot; empty 'token' field in 'discord' section of the configuration file")
	}
}

func (dab *DownArrowsBot) initStorage() {
	dab.Logger.Print("using database ", dab.conf.Database.Path)
	dab.components.Storage = NewStorage(dab.conf.Database)
	dab.components.Enabled = append(dab.components.Enabled, "storage")
}

func (dab *DownArrowsBot) initReddit(ctx context.Context) {
	user_agent, err := template.New("UserAgent").Parse(dab.conf.Reddit.UserAgent)
	if err != nil {
		dab.Logger.Fatal(err)
	}

	dab.Logger.Print("attempting to log into reddit")
	ra, err := NewRedditAPI(ctx, dab.conf.Reddit.RedditAuth, user_agent)
	if err != nil {
		dab.Logger.Fatal(err)
	}
	dab.Logger.Print("successfully logged into reddit")

	reddit_logger := log.New(dab.logOut, "", dab.loggerOpts)

	dab.components.RedditScanner = NewRedditScanner(reddit_logger, dab.components.Storage, ra, dab.conf.Reddit.RedditScannerConf)
	dab.components.RedditUsers = NewRedditUsers(reddit_logger, dab.components.Storage, ra)
	dab.components.RedditSubs = NewRedditSubs(reddit_logger, dab.components.Storage, ra)
}

func (dab *DownArrowsBot) initReport() {
	dab.components.Report = NewReportFactory(dab.components.Storage, dab.conf.Report)
}

func (dab *DownArrowsBot) report() {
	dab.Logger.Print("printing report for last week")
	year, week := dab.components.Report.LastWeekCoordinates()
	report := dab.components.Report.ReportWeek(year, week)
	if report.Len() == 0 {
		dab.Logger.Fatal("empty report")
	}
	autopanic(WriteMarkdownReport(report, dab.stdOut))
}

func (dab *DownArrowsBot) userAdd(ctx context.Context) {
	usernames := dab.flagSet.Args()
	for _, username := range usernames {
		hidden := strings.HasPrefix(username, dab.conf.HidePrefix)
		username = strings.TrimPrefix(username, dab.conf.HidePrefix)
		if res := dab.components.RedditUsers.AddUser(ctx, username, hidden, true); res.Error != nil && !res.Exists {
			dab.Logger.Fatal(res.Error)
		}
	}
}

func (dab *DownArrowsBot) launchReddit(ctx context.Context) {

	if dab.runtimeConf.Compendium {
		dab.withShutdown(func() {
			if err := dab.components.RedditUsers.UpdateUsersFromCompendium(ctx); err != nil {
				dab.Logger.Print(err)
			}
		})
	}

	go dab.withShutdown(func() {
		err := dab.components.RedditScanner.Run(ctx)
		if err != nil && !isContextError(err) {
			dab.Logger.Print(err)
		}
	})

	go dab.withShutdown(func() {
		dab.components.RedditUsers.AutoCompendiumUpdate(ctx, dab.conf.Reddit.CompendiumUpdateInterval.Value)
	})

	dab.Daemon = true
}

func (dab *DownArrowsBot) launchDiscord() {
	var err error

	dab.Logger.Print("attempting to connect discord bot")
	bot_logger := log.New(dab.logOut, "", dab.loggerOpts)

	if dab.conf.Discord.Timezone.Value == nil {
		dab.conf.Discord.Timezone.Value = dab.runtimeConf.Timezone
	}

	dab.components.Discord, err = NewDiscordBot(dab.components.Storage, bot_logger, dab.conf.Discord.DiscordBotConf)
	if err != nil {
		dab.Logger.Fatal(err)
	}
	if err := dab.components.Discord.Run(); err != nil {
		dab.Logger.Fatal(err)
	}

	dab.Daemon = true
	dab.Logger.Print("discord bot connected")
}

func (dab *DownArrowsBot) connectRedditAndDiscord(ctx context.Context) {
	dab.Logger.Print("connecting the discord bot and the reddit bot together")
	go dab.withShutdown(func() {
		dab.components.RedditUsers.AddUserServer(ctx, dab.components.Discord.AddUser)
	})

	if dab.conf.Reddit.DVTInterval.Value > 0*time.Second {
		reddit_evts := make(chan Comment)
		go dab.withShutdown(func() { dab.components.Discord.RedditEvents(reddit_evts) })
		go dab.withShutdown(func() {
			dab.components.RedditSubs.StreamSub(ctx, "downvote_trolls", reddit_evts, dab.conf.Reddit.DVTInterval.Value)
		})
	}

	suspensions := dab.components.RedditScanner.Suspensions() // suspensions are actually watched by the scanning of comments, not here
	dab.components.Discord.SignalSuspensions(suspensions)

	unsuspensions := make(chan User)
	go dab.withShutdown(func() {
		dab.components.RedditUsers.CheckUnsuspendedAndNotFound(ctx, dab.conf.Reddit.UnsuspensionInterval.Value, unsuspensions)
	})
	go dab.components.Discord.SignalUnsuspensions(unsuspensions)

	if dab.conf.Discord.HighScores != "" {
		highscores := dab.components.RedditScanner.HighScores() // this also happens during the scanning of comments
		go dab.components.Discord.SignalHighScores(highscores)
	}
	dab.Logger.Print("discord bot and reddit bot connected")
}

func (dab *DownArrowsBot) checkWebConf() {
	if dab.conf.Web.Listen != "" {
		dab.runtimeConf.Web = true
		dab.components.Enabled = append(dab.components.Enabled, "web server")
	} else {
		dab.Logger.Print("disabling web server; empty 'listen' field in 'web' section of the configuration file")
	}
}

func (dab *DownArrowsBot) launchWeb() {
	dab.Logger.Print("lauching the web server")
	dab.components.Web = NewWebServer(dab.conf.Web, dab.components.Report, dab.components.Storage)
	go func() {
		err := dab.components.Web.Run()
		if err != nil {
			dab.Logger.Print(err)
		}
	}()
	dab.Daemon = true
}
