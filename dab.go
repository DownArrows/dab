package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"strings"
	"text/template"
)

var Version = SemVer{1, 6, 2}

type DownArrowsBot struct {
	flagSet    *flag.FlagSet
	logger     *log.Logger
	loggerOpts int
	logOut     io.Writer
	stdOut     io.Writer

	conf Configuration

	runtimeConf struct {
		InitDB  bool
		Report  bool
		UserAdd bool
	}

	layers struct {
		Storage *Storage
		Report  ReportFactory
		// RedditAPI is also a layer but is passed around as an argument instead
	}

	components struct {
		ConfState     ComponentsConf
		Discord       *DiscordBot
		RedditScanner *RedditScanner
		RedditUsers   *RedditUsers
		RedditSubs    *RedditSubs
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

func (dab *DownArrowsBot) Run(ctx context.Context, args []string) error {
	if err := dab.init(args); err != nil {
		return err
	}

	if err := dab.conf.HasSaneValues(); err != nil {
		return err
	}

	dab.logger.Print("using database ", dab.conf.Database.Path)
	if storage, err := NewStorage(dab.logger, dab.conf.Database); err != nil {
		return err
	} else {
		dab.layers.Storage = storage
		defer dab.layers.Storage.Close()
	}

	if dab.runtimeConf.InitDB {
		return nil
	}

	dab.layers.Report = NewReportFactory(dab.layers.Storage, dab.conf.Report)
	if dab.runtimeConf.Report {
		return dab.report()
	}

	if dab.runtimeConf.UserAdd {
		if !dab.components.ConfState.Reddit.Enabled {
			return dab.components.ConfState.Reddit.Error
		}
		return dab.userAdd(ctx)
	}

	connectors := NewTaskGroup(ctx)

	if dab.components.ConfState.Reddit.Enabled {
		connectors.Spawn(dab.connectReddit)
	}

	if dab.components.ConfState.Discord.Enabled {
		connectors.Spawn(dab.connectDiscord)
	}

	if err := connectors.Wait().ToError(); err != nil {
		return err
	}
	if IsCancellation(ctx.Err()) {
		return nil
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

	if dab.layers.Storage.PeriodicCleanupEnabled {
		top_level.Spawn(dab.layers.Storage.PeriodicCleanup)
	}

	if dab.components.ConfState.Reddit.Enabled {
		writers.Spawn(dab.components.RedditScanner.Run)
		if dab.components.RedditUsers.AutoUpdateUsersFromCompendiumEnabled {
			writers.Spawn(dab.components.RedditUsers.AutoUpdateUsersFromCompendium)
		}
	}

	if dab.components.ConfState.Discord.Enabled {
		writers.Spawn(dab.components.Discord.Run)
	}

	if dab.components.ConfState.Reddit.Enabled && dab.components.ConfState.Discord.Enabled {
		dab.connectRedditAndDiscord(readers, writers)
	}

	if dab.components.ConfState.Web.Enabled {
		dab.components.Web = NewWebServer(dab.conf.Web, dab.layers.Report, dab.layers.Storage)
		top_level.Spawn(dab.components.Web.Run)
	}

	dab.logger.Print(dab.components.ConfState)

	return top_level.Wait().ToError()
}

func (dab *DownArrowsBot) init(args []string) error {
	path := dab.flagSet.String("config", "./dab.conf.json", "Path to the configuration file.")
	dab.flagSet.BoolVar(&dab.runtimeConf.InitDB, "initdb", false, "Initialize the database and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.Report, "report", false, "Print the report for the last week and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.UserAdd, "useradd", false, "Add one or multiple usernames to be tracked and exit.")
	dab.flagSet.Parse(args)

	if !dab.runtimeConf.UserAdd && dab.flagSet.NArg() > 0 {
		return errors.New("no argument besides usernames when adding users is accepted")
	}

	conf, err := NewConfiguration(*path)
	dab.conf = conf
	dab.components.ConfState = conf.Components()

	return err
}

func (dab *DownArrowsBot) makeRedditAPI(ctx context.Context) (*RedditAPI, error) {
	user_agent, err := template.New("UserAgent").Parse(dab.conf.Reddit.UserAgent)
	if err != nil {
		return nil, err
	}

	dab.logger.Print("attempting to log into reddit")
	ra, err := NewRedditAPI(ctx, dab.conf.Reddit.RedditAuth, user_agent)
	if err != nil {
		return nil, err
	}
	dab.logger.Print("successfully logged into reddit")

	return ra, nil
}

func (dab *DownArrowsBot) connectReddit(ctx context.Context) error {
	ra, err := dab.makeRedditAPI(ctx)
	if err != nil {
		return err
	}
	dab.components.RedditScanner = NewRedditScanner(dab.logger, dab.layers.Storage, ra, dab.conf.Reddit.RedditScannerConf)
	dab.components.RedditUsers = NewRedditUsers(dab.logger, dab.layers.Storage, ra, dab.conf.Reddit.RedditUsersConf)
	dab.components.RedditSubs = NewRedditSubs(dab.logger, dab.layers.Storage, ra)

	return nil
}

func (dab *DownArrowsBot) connectDiscord(ctx context.Context) error {
	dab.logger.Print("attempting to log into discord")
	bot, err := NewDiscordBot(dab.layers.Storage, dab.logger, dab.conf.Discord.DiscordBotConf)
	if err != nil {
		return err
	}
	dab.logger.Print("successfully logged into discord")
	dab.components.Discord = bot
	return nil
}

func (dab *DownArrowsBot) connectRedditAndDiscord(readers *TaskGroup, writers *TaskGroup) {
	writers.Spawn(func(ctx context.Context) error {
		return dab.components.RedditUsers.AddUserServer(ctx, dab.components.Discord.AddUser)
	})

	if dab.conf.Reddit.DVTInterval.Value > 0 {
		reddit_evts := make(chan Comment)
		readers.Spawn(func(ctx context.Context) error {
			return dab.components.Discord.SignalNewRedditPosts(ctx, reddit_evts)
		})
		writers.Spawn(func(ctx context.Context) error {
			return dab.components.RedditSubs.NewPostsOnSub(ctx, "downvote_trolls", reddit_evts, dab.conf.Reddit.DVTInterval.Value)
		})
	}

	suspensions := dab.components.RedditScanner.Suspensions()
	readers.Spawn(func(ctx context.Context) error {
		return dab.components.Discord.SignalSuspensions(ctx, suspensions)
	})

	if dab.components.RedditUsers.UnsuspensionWatcherEnabled {
		writers.Spawn(dab.components.RedditUsers.UnsuspensionWatcher)
		readers.Spawn(func(ctx context.Context) error {
			return dab.components.Discord.SignalUnsuspensions(ctx, dab.components.RedditUsers.Unsuspensions())
		})
	}

	if dab.conf.Discord.HighScores != "" {
		highscores := dab.components.RedditScanner.HighScores()
		readers.Spawn(func(ctx context.Context) error {
			return dab.components.Discord.SignalHighScores(ctx, highscores)
		})
	}
}

func (dab *DownArrowsBot) report() error {
	dab.logger.Print("printing report for last week")
	year, week := dab.layers.Report.LastWeekCoordinates()
	report := dab.layers.Report.ReportWeek(year, week)
	if report.Len() == 0 {
		return errors.New("empty report")
	}
	return WriteMarkdownReport(report, dab.stdOut)
}

func (dab *DownArrowsBot) userAdd(ctx context.Context) error {
	ra, err := dab.makeRedditAPI(ctx)
	if err != nil {
		return err
	}

	ru := NewRedditUsers(dab.logger, dab.layers.Storage, ra, dab.conf.Reddit.RedditUsersConf)

	usernames := dab.flagSet.Args()
	for _, username := range usernames {
		hidden := strings.HasPrefix(username, dab.conf.HidePrefix)
		username = strings.TrimPrefix(username, dab.conf.HidePrefix)
		if res := ru.AddUser(ctx, username, hidden, true); res.Error != nil && !res.Exists {
			return res.Error
		}
	}
	return nil
}
