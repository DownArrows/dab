package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"strings"
	"sync"
	"text/template"
	"time"
)

const Version = "0.247"

type DownArrowsBot struct {
	conf Configuration

	flagSet    *flag.FlagSet
	logger     *log.Logger
	loggerOpts int
	logOut     io.Writer
	stdOut     io.Writer

	runtimeConf struct {
		Compendium bool
		Discord    bool
		InitDB     bool
		Launched   bool
		Reddit     bool
		Report     bool
		UserAdd    bool
		Web        bool
	}

	components struct {
		sync.Mutex
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
		flagSet:    flag.NewFlagSet("DownArrowsBot", flag.ExitOnError),
		logger:     log.New(log_out, "", logger_opts),
		loggerOpts: logger_opts,
		logOut:     log_out,
		stdOut:     output,
	}
}

func (dab *DownArrowsBot) enable(name string) {
	dab.components.Lock()
	defer dab.components.Unlock()
	dab.components.Enabled = append(dab.components.Enabled, name)
}

func (dab *DownArrowsBot) Run(ctx context.Context, args []string) error {
	if err := dab.init(args); err != nil {
		return err
	}

	dab.logger.Print("using database ", dab.conf.Database.Path)
	if storage, err := NewStorage(dab.conf.Database); err != nil {
		return err
	} else {
		dab.components.Storage = storage
		dab.enable("storage")
		defer dab.components.Storage.Close()
	}

	if dab.runtimeConf.InitDB {
		return nil
	}

	dab.components.Report = NewReportFactory(dab.components.Storage, dab.conf.Report)
	if dab.runtimeConf.Report {
		return dab.report()
	}

	connectors := NewTaskGroup(ctx)

	dab.checkRedditConf()
	if dab.runtimeConf.Reddit {
		connectors.Spawn(dab.connectReddit)
	}

	dab.checkDiscordConf()
	if dab.runtimeConf.Discord {
		connectors.Spawn(dab.connectDiscord)
	}

	if err := connectors.Wait().ToError(); err != nil {
		return err
	}
	if isCancellation(ctx.Err()) {
		return nil
	}

	if dab.runtimeConf.UserAdd {
		if !dab.runtimeConf.Reddit {
			return errors.New("reddit bot must be running to register users")
		}
		return dab.userAdd(ctx)
	}

	top_level := NewTaskGroup(ctx)
	readers := NewTaskGroup(context.Background())
	writers := NewTaskGroup(context.Background())

	top_level.Spawn(func(ctx context.Context) error {
		<-ctx.Done()
		writers.Cancel()
		if err := writers.Wait().ToError(); err != nil {
			readers.Cancel() // Readers might never return due to the error, so don't wait
			return err
		}
		readers.Cancel()
		return readers.Wait().ToError()
	})

	top_level.Spawn(dab.components.Storage.PeriodicVacuum)

	if dab.runtimeConf.Reddit {
		writers.Spawn(dab.components.RedditScanner.Run)
		writers.Spawn(dab.components.RedditUsers.AutoUpdateUsersFromCompendium)
	}

	if dab.runtimeConf.Discord {
		top_level.Spawn(dab.components.Discord.Run)
	}

	if dab.runtimeConf.Reddit && dab.runtimeConf.Discord {
		dab.connectRedditAndDiscord(readers, writers)
	}

	dab.checkWebConf()
	if dab.runtimeConf.Web {
		dab.components.Web = NewWebServer(dab.conf.Web, dab.components.Report, dab.components.Storage)
		top_level.Spawn(dab.components.Web.Run)
	}

	dab.runtimeConf.Launched = true
	dab.logger.Print("launched the following components: ", strings.Join(dab.components.Enabled, ", "))

	return top_level.Wait().ToError()
}

func (dab *DownArrowsBot) init(args []string) error {
	path := dab.flagSet.String("config", "./dab.conf.json", "Path to the configuration file.")
	dab.flagSet.BoolVar(&dab.runtimeConf.Compendium, "compendium", false, "Start the reddit bot with an update from DVT's compendium.")
	dab.flagSet.BoolVar(&dab.runtimeConf.InitDB, "initdb", false, "Initialize the database and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.Report, "report", false, "Print the report for the last week and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.UserAdd, "useradd", false, "Add one or multiple usernames to be tracked and exit.")
	dab.flagSet.Parse(args)

	if !dab.runtimeConf.UserAdd && dab.flagSet.NArg() > 0 {
		return errors.New("No argument besides usernames when adding users is accepted")
	}

	conf, err := NewConfiguration(*path)
	dab.conf = conf
	return err
}

func (dab *DownArrowsBot) checkRedditConf() {
	if dab.conf.Reddit.Username == "" || dab.conf.Reddit.Secret == "" || dab.conf.Reddit.Id == "" ||
		dab.conf.Reddit.Password == "" || dab.conf.Reddit.UserAgent == "" {
		fields := "id, secret, username, password, user_agent"
		msg := "Disabling reddit bot; at least one of the required fields of 'reddit' in the configuration file is empty"
		dab.logger.Print(msg, ": ", fields)
	} else {
		dab.runtimeConf.Reddit = true
		dab.enable("reddit")
	}
}

func (dab *DownArrowsBot) checkDiscordConf() {
	if dab.conf.Discord.Token != "" {
		dab.runtimeConf.Discord = true
		dab.enable("discord")
	} else {
		dab.logger.Print("disabling discord bot; empty 'token' field in 'discord' section of the configuration file")
	}
}

func (dab *DownArrowsBot) checkWebConf() {
	if dab.conf.Web.Listen != "" {
		dab.runtimeConf.Web = true
		dab.enable("web server")
	} else {
		dab.logger.Print("disabling web server; empty 'listen' field in 'web' section of the configuration file")
	}
}

func (dab *DownArrowsBot) initStorage() {
}

func (dab *DownArrowsBot) connectReddit(ctx context.Context) error {
	user_agent, err := template.New("UserAgent").Parse(dab.conf.Reddit.UserAgent)
	if err != nil {
		return err
	}

	dab.logger.Print("attempting to log into reddit")
	ra, err := NewRedditAPI(ctx, dab.conf.Reddit.RedditAuth, user_agent)
	if err != nil {
		return err
	}
	dab.logger.Print("successfully logged into reddit")

	dab.components.RedditScanner = NewRedditScanner(dab.logger, dab.components.Storage, ra, dab.conf.Reddit.RedditScannerConf)
	dab.components.RedditUsers = NewRedditUsers(dab.logger, dab.components.Storage, ra, dab.conf.Reddit.RedditUsersConf)
	dab.components.RedditSubs = NewRedditSubs(dab.logger, dab.components.Storage, ra)

	return nil
}

func (dab *DownArrowsBot) connectDiscord(ctx context.Context) error {
	dab.logger.Print("connecting discord bot")
	bot, err := NewDiscordBot(dab.components.Storage, dab.logger, dab.conf.Discord.DiscordBotConf)
	if err != nil {
		return err
	}
	dab.components.Discord = bot
	return nil
}

func (dab *DownArrowsBot) connectRedditAndDiscord(readers *TaskGroup, writers *TaskGroup) {
	writers.Spawn(func(ctx context.Context) error {
		return dab.components.RedditUsers.AddUserServer(ctx, dab.components.Discord.AddUser)
	})

	if dab.conf.Reddit.DVTInterval.Value > 0*time.Second {
		reddit_evts := make(chan Comment)
		readers.Spawn(func(ctx context.Context) error { return dab.components.Discord.RedditEvents(ctx, reddit_evts) })
		writers.Spawn(func(ctx context.Context) error {
			return dab.components.RedditSubs.StreamSub(ctx, "downvote_trolls", reddit_evts, dab.conf.Reddit.DVTInterval.Value)
		})
	}

	suspensions := dab.components.RedditScanner.Suspensions() // suspensions are actually watched by the scanning of comments, not here
	readers.Spawn(func(ctx context.Context) error {
		return dab.components.Discord.SignalSuspensions(ctx, suspensions)
	})

	writers.Spawn(dab.components.RedditUsers.CheckUnsuspendedAndNotFound)
	readers.Spawn(func(ctx context.Context) error {
		return dab.components.Discord.SignalUnsuspensions(ctx, dab.components.RedditUsers.Unsuspensions)
	})

	if dab.conf.Discord.HighScores != "" {
		highscores := dab.components.RedditScanner.HighScores() // this also happens during the scanning of comments
		readers.Spawn(func(ctx context.Context) error {
			return dab.components.Discord.SignalHighScores(ctx, highscores)
		})
	}
}

func (dab *DownArrowsBot) report() error {
	dab.logger.Print("printing report for last week")
	year, week := dab.components.Report.LastWeekCoordinates()
	report := dab.components.Report.ReportWeek(year, week)
	if report.Len() == 0 {
		return errors.New("empty report")
	}
	return WriteMarkdownReport(report, dab.stdOut)
}

func (dab *DownArrowsBot) userAdd(ctx context.Context) error {
	usernames := dab.flagSet.Args()
	for _, username := range usernames {
		hidden := strings.HasPrefix(username, dab.conf.HidePrefix)
		username = strings.TrimPrefix(username, dab.conf.HidePrefix)
		if res := dab.components.RedditUsers.AddUser(ctx, username, hidden, true); res.Error != nil && !res.Exists {
			return res.Error
		}
	}
	return nil
}
