package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"text/template"
)

var Version = SemVer{1, 8, 0}

const DefaultChannelSize = 100

// This data structure and its methods contain very little logic.
// All it does is pass dependencies around and connect components
// together according to what is already decided in the configuration
// data structure. It offers a clear view of how everything is organized.
type DownArrowsBot struct {
	flagSet *flag.FlagSet
	logger  LevelLogger
	logLvl  string
	logOut  io.Writer
	stdOut  io.Writer

	runtimeConf struct {
		ConfPath string
		InitDB   bool
		Report   bool
		UserAdd  bool
	}

	conf Configuration

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

func NewDownArrowsBot(log_out io.Writer, output io.Writer) *DownArrowsBot {
	dab := &DownArrowsBot{
		flagSet: flag.NewFlagSet("DownArrowsBot", flag.ExitOnError),
		stdOut:  output,
	}
	dab.logOut = log_out
	return dab
}

func (dab *DownArrowsBot) Run(ctx context.Context, args []string) error {
	if err := dab.parseFlags(args); err != nil {
		return err
	}

	if logger, err := NewLevelLogger(dab.logOut, dab.logLvl); err != nil {
		return err
	} else {
		dab.logger = logger
	}

	dab.logger.Infof("running DAB version %s", Version)

	// Most of the decisions about what parts of the code
	// should be enabled is done there.
	if conf, err := NewConfiguration(dab.runtimeConf.ConfPath); err != nil {
		return err
	} else {
		dab.conf = conf
		dab.components.ConfState = conf.Components()
	}

	if err := dab.conf.HasSaneValues(); err != nil {
		return err
	}

	if dab.conf.Discord.Admin != "" {
		dab.logger.Info("discord.admin is deprecated, use discord.privileged_role instead")
	}

	dab.logger.Infof("using database %s", dab.conf.Database.Path)
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

	tasks := NewTaskGroup(ctx)

	dab.logger.Info(dab.components.ConfState)

	if dab.components.ConfState.Web.Enabled {
		dab.components.Web = NewWebServer(dab.conf.Web, dab.layers.Report, dab.layers.Storage)
		tasks.SpawnCtx(dab.components.Web.Run)
	}

	if dab.layers.Storage.PeriodicCleanupEnabled {
		tasks.SpawnCtx(dab.layers.Storage.PeriodicCleanup)
	}

	if dab.components.ConfState.Reddit.Enabled {
		reddit_api, err := dab.makeRedditAPI(ctx)
		if err != nil {
			return err
		}

		dab.components.RedditScanner = NewRedditScanner(dab.logger, dab.layers.Storage, reddit_api, dab.conf.Reddit.RedditScannerConf)
		dab.components.RedditUsers = NewRedditUsers(dab.logger, dab.layers.Storage, reddit_api, dab.conf.Reddit.RedditUsersConf)
		dab.components.RedditSubs = NewRedditSubs(dab.logger, dab.layers.Storage, reddit_api)

		tasks.SpawnCtx(func(ctx context.Context) error {
			dab.logger.Info("attempting to log into reddit")
			if err := reddit_api.Connect(ctx); err != nil {
				if !IsCancellation(err) {
					dab.logger.Infof("failed to log into reddit: %v", err)
				}
				return err
			}
			dab.logger.Info("successfully logged into reddit")

			tasks.SpawnCtx(dab.components.RedditScanner.Run)

			if dab.components.RedditUsers.AutoUpdateUsersFromCompendiumEnabled {
				tasks.SpawnCtx(dab.components.RedditUsers.AutoUpdateUsersFromCompendium)
			}

			return nil
		})
	}

	var err error
	dab.components.Discord, err = NewDiscordBot(dab.layers.Storage, dab.logger, dab.conf.Discord.DiscordBotConf)
	if err != nil {
		return err
	}

	if dab.components.ConfState.Discord.Enabled {
		tasks.SpawnCtx(func(ctx context.Context) error {
			dab.logger.Info("attempting to log into discord")
			return dab.components.Discord.Run(ctx)
		})
	}

	if dab.components.ConfState.Reddit.Enabled && dab.components.ConfState.Discord.Enabled {

		tasks.SpawnCtx(func(ctx context.Context) error {
			defer dab.components.Discord.CloseAddUser()
			return dab.components.RedditUsers.AddUserServer(ctx, dab.components.Discord.OpenAddUser())
		})

		if dab.conf.Reddit.DVTInterval.Value > 0 {
			reddit_evts := make(chan Comment)
			tasks.Spawn(func() { dab.components.Discord.SignalNewRedditPosts(reddit_evts) })
			tasks.SpawnCtx(func(ctx context.Context) error {
				defer close(reddit_evts)
				return dab.components.RedditSubs.NewPostsOnSub(ctx, "downvote_trolls", reddit_evts, dab.conf.Reddit.DVTInterval.Value)
			})
		}

		tasks.Spawn(func() { dab.components.Discord.SignalSuspensions(dab.components.RedditScanner.OpenSuspensions()) })

		if dab.components.RedditUsers.UnsuspensionWatcherEnabled {
			tasks.SpawnCtx(dab.components.RedditUsers.UnsuspensionWatcher)
			tasks.Spawn(func() { dab.components.Discord.SignalUnsuspensions(dab.components.RedditUsers.Unsuspensions()) })
		}

		if dab.conf.Discord.HighScores != "" {
			tasks.Spawn(func() { dab.components.Discord.SignalHighScores(dab.components.RedditScanner.OpenHighScores()) })
		}

		tasks.SpawnCtx(func(ctx context.Context) error {
			<-ctx.Done()
			dab.components.RedditScanner.CloseSuspensions()
			dab.components.RedditScanner.CloseHighScores()
			return ctx.Err()
		})
	}

	return tasks.Wait().ToError()
}

func (dab *DownArrowsBot) parseFlags(args []string) error {
	dab.flagSet.StringVar(&dab.logLvl, "log", "Info", "Logging level ("+strings.Join(LevelLoggerLevels, ", ")+").")
	dab.flagSet.StringVar(&dab.runtimeConf.ConfPath, "config", "./dab.conf.json", "Path to the configuration file.")
	dab.flagSet.BoolVar(&dab.runtimeConf.InitDB, "initdb", false, "Initialize the database and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.Report, "report", false, "Print the report for the last week and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.UserAdd, "useradd", false, "Add one or multiple usernames to be tracked and exit.")
	dab.flagSet.Parse(args)

	if !dab.runtimeConf.UserAdd && dab.flagSet.NArg() > 0 {
		return errors.New("no argument besides usernames when adding users is accepted")
	}

	return nil
}

func (dab *DownArrowsBot) makeRedditAPI(ctx context.Context) (*RedditAPI, error) {
	user_agent, err := template.New("UserAgent").Parse(dab.conf.Reddit.UserAgent)
	if err != nil {
		return nil, err
	}

	ra, err := NewRedditAPI(ctx, dab.conf.Reddit.RedditAuth, user_agent)
	if err != nil {
		return nil, err
	}

	return ra, nil
}

func (dab *DownArrowsBot) report() error {
	dab.logger.Info("printing report for last week")
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
