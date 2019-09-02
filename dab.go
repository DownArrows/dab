package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"regexp"
	"strings"
	"text/template"
)

// Version of the application.
var Version = SemVer{1, 18, 3}

// DefaultChannelSize is the size of the channels that are used throughout of the application, unless there's a need for a specific size.
const DefaultChannelSize = 100

var userAddSeparators = regexp.MustCompile("[ ,]")

// DownArrowsBot and its methods contain very little logic.
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
		UserAdd  string
	}

	conf Configuration

	layers struct {
		Storage    *Storage
		Report     ReportFactory
		Compendium CompendiumFactory
		// RedditAPI is also a layer but is passed around as an argument instead
	}

	components struct {
		ConfState     ComponentsConf
		Discord       *DiscordBot
		RedditScanner *RedditScanner
		RedditUsers   *RedditUsers
		Web           *WebServer
	}
}

// NewDownArrowsBot creates a new DownArrowsBot.
// logOut is the output of the logs, and output the output for other data.
// Typically logOut will be stderr, and output stdout.
func NewDownArrowsBot(logOut io.Writer, output io.Writer) *DownArrowsBot {
	dab := &DownArrowsBot{
		flagSet: flag.NewFlagSet("DownArrowsBot", flag.ContinueOnError),
		stdOut:  output,
	}
	dab.logOut = logOut
	return dab
}

// Run launches a DownArrowsBot with the given args and blocks until it is shutdown.
func (dab *DownArrowsBot) Run(ctx context.Context, args []string) error {
	var err error

	if err := dab.parseFlags(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	var logger LevelLogger
	if logger, err = NewStdLevelLogger(dab.logOut, dab.logLvl); err != nil {
		return err
	}
	dab.logger = logger

	dab.logger.Infof("running DAB version %s", Version)

	// Most of the decisions about what parts of the code
	// should be enabled is done there.
	var conf Configuration
	if conf, err = NewConfiguration(dab.runtimeConf.ConfPath); err != nil {
		return err
	}
	dab.conf = conf
	dab.components.ConfState = conf.Components()

	if err := dab.conf.HasSaneValues(); err != nil {
		return err
	}

	for _, msg := range dab.conf.Deprecations() {
		dab.logger.Info(msg)
	}

	dab.logger.Infof("using database %s", dab.conf.Database.Path)
	var storage *Storage
	if storage, err = NewStorage(ctx, dab.logger, dab.conf.Database); err != nil {
		return err
	}
	dab.layers.Storage = storage

	if dab.runtimeConf.InitDB {
		return nil
	}

	dab.layers.Report = NewReportFactory(dab.conf.Report)
	if dab.runtimeConf.Report {
		return dab.report(ctx)
	}

	if dab.runtimeConf.UserAdd != "" {
		if !dab.components.ConfState.Reddit.Enabled {
			return dab.components.ConfState.Reddit.Error
		}
		return dab.userAdd(ctx)
	}

	dab.layers.Compendium = NewCompendiumFactory(dab.conf.Compendium)

	tasks := NewTaskGroup(ctx)

	dab.logger.Info(dab.components.ConfState)

	if dab.components.ConfState.Web.Enabled {
		dab.components.Web = NewWebServer(dab.logger, dab.layers.Storage, dab.layers.Report, dab.layers.Compendium, dab.conf.Web)
		tasks.SpawnCtx(dab.components.Web.Run)
	}

	if dab.layers.Storage.PeriodicCleanupIsEnabled() {
		tasks.SpawnCtx(dab.layers.Storage.PeriodicCleanup)
	}

	if dab.components.ConfState.Reddit.Enabled {
		redditAPI, err := dab.makeRedditAPI(ctx)
		if err != nil {
			return err
		}

		dab.components.RedditScanner = NewRedditScanner(dab.logger, dab.layers.Storage, redditAPI, dab.conf.Reddit.RedditScannerConf)
		dab.components.RedditUsers = NewRedditUsers(dab.logger, redditAPI, dab.conf.Reddit.RedditUsersConf)

		retrier := NewRetrier(dab.conf.Reddit.Retry, func(r *Retrier, err error) {
			dab.logger.Errorf("error in reddit component, restarting (%d retries, %s backoff): %v", r.Retries, r.Backoff, err)
		})

		tasks.SpawnCtx(retrier.Set(func(ctx context.Context) error {
			dab.logger.Info("attempting to log into reddit")
			if err := redditAPI.Connect(ctx); err != nil {
				return err
			}
			dab.logger.Info("successfully logged into reddit")

			return dab.components.RedditScanner.Run(ctx)
		}).Task)
	}

	if dab.components.ConfState.Discord.Enabled {
		var err error

		discordConn, err := dab.layers.Storage.GetConn(ctx)
		if err != nil {
			return err
		}

		dab.components.Discord, err = NewDiscordBot(dab.logger, discordConn, dab.components.RedditUsers.Add, dab.conf.Discord.DiscordBotConf)
		if err != nil {
			return err
		}

		retrier := NewRetrier(dab.conf.Discord.Retry, func(r *Retrier, err error) {
			dab.logger.Errorf("error in discord component, restarting (%d retries, %s backoff): %v", r.Retries, r.Backoff, err)
		})

		tasks.SpawnCtx(retrier.Set(func(ctx context.Context) error {
			dab.logger.Info("attempting to log into discord")
			return dab.components.Discord.Run(ctx)
		}).Task)
	}

	if dab.components.ConfState.Reddit.Enabled && dab.components.ConfState.Discord.Enabled {

		tasks.Spawn(func() { dab.components.Discord.SignalSuspensions(dab.components.RedditScanner.OpenSuspensions()) })

		if dab.components.RedditUsers.UnsuspensionWatcherEnabled {
			tasks.SpawnCtx(func(ctx context.Context) error {
				return dab.layers.Storage.WithConn(ctx, func(conn StorageConn) error {
					return dab.components.RedditUsers.UnsuspensionWatcher(ctx, conn)
				})
			})
			tasks.Spawn(func() { dab.components.Discord.SignalUnsuspensions(dab.components.RedditUsers.OpenUnsuspensions()) })
		}

		if dab.conf.Discord.HighScores != "" {
			tasks.Spawn(func() { dab.components.Discord.SignalHighScores(dab.components.RedditScanner.OpenHighScores()) })
		}

		tasks.SpawnCtx(func(ctx context.Context) error {
			<-ctx.Done()
			dab.components.RedditUsers.CloseUnsuspensions()
			dab.components.RedditScanner.CloseSuspensions()
			dab.components.RedditScanner.CloseHighScores()
			return ctx.Err()
		})
	}

	return tasks.Wait().ToError()
}

func (dab *DownArrowsBot) parseFlags(args []string) error {
	dab.flagSet.SetOutput(dab.stdOut)
	dab.flagSet.StringVar(&dab.logLvl, "log", "Info", "Logging level ("+strings.Join(LevelLoggerLevels, ", ")+").")
	dab.flagSet.StringVar(&dab.runtimeConf.ConfPath, "config", "./dab.conf.json", "Path to the configuration file.")
	dab.flagSet.BoolVar(&dab.runtimeConf.InitDB, "initdb", false, "Initialize the database and exit.")
	dab.flagSet.BoolVar(&dab.runtimeConf.Report, "report", false, "Print the report for the last week and exit (deprecated).")
	dab.flagSet.StringVar(&dab.runtimeConf.UserAdd, "useradd", "",
		"Add one or multiple usernames separated by a white space or a comma to be tracked and exit.")
	if err := dab.flagSet.Parse(args); err != nil {
		return err
	}

	if dab.flagSet.NArg() > 0 {
		return errors.New("the application doesn't take any argument other than those given to its options as single strings")
	}

	if dab.runtimeConf.Report {
		dab.logger.Info("the -report option has been superseded by the the web reports and is deprecated")
	}

	return nil
}

func (dab *DownArrowsBot) makeRedditAPI(ctx context.Context) (*RedditAPI, error) {
	userAgent, err := template.New("UserAgent").Parse(dab.conf.Reddit.UserAgent)
	if err != nil {
		return nil, err
	}

	ra, err := NewRedditAPI(ctx, dab.conf.Reddit.RedditAuth, userAgent)
	if err != nil {
		return nil, err
	}

	return ra, nil
}

func (dab *DownArrowsBot) report(ctx context.Context) error {
	conn, err := dab.layers.Storage.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	dab.logger.Info("printing report for last week")

	year, week := dab.layers.Report.LastWeekCoordinates()
	report, err := dab.layers.Report.ReportWeek(conn, year, week)
	if err != nil {
		return err
	} else if report.Len() == 0 {
		return errors.New("empty report")
	}

	return MarkdownReport.Execute(dab.stdOut, report)
}

func (dab *DownArrowsBot) userAdd(ctx context.Context) error {
	ra, err := dab.makeRedditAPI(ctx)
	if err != nil {
		return err
	}

	if err := ra.Connect(ctx); err != nil {
		return err
	}

	ru := NewRedditUsers(dab.logger, ra, dab.conf.Reddit.RedditUsersConf)

	conn, err := dab.layers.Storage.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	usernames := userAddSeparators.Split(dab.runtimeConf.UserAdd, -1)
	for _, username := range usernames {
		hidden := strings.HasPrefix(username, dab.conf.HidePrefix)
		username = strings.TrimPrefix(username, dab.conf.HidePrefix)
		if res := ru.Add(ctx, conn, username, hidden, true); res.Error != nil {
			dab.logger.Errorf("error when trying to register %q: %v", username, res.Error)
		} else if !res.Exists {
			dab.logger.Errorf("user %q not found on Reddit", username)
		} else {
			dab.logger.Infof("successfully registered user %q", res.User.Name)
		}
	}
	return nil
}
