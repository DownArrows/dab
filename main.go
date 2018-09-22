package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const logger_opts int = log.Lshortfile

func main() {
	logger := log.New(os.Stderr, "", logger_opts)

	defer logger.Print("DAB stopped")

	report := flag.Bool("report", false, "Print the report for the last week and exit.")
	useradd := flag.Bool("useradd", false, "Add one or multiple usernames to be tracked and exit.")
	config_path := flag.String("config", "./dab.conf.json", "Path to the configuration file.")
	compendium := flag.Bool("compendium", false, "Start the reddit bot with an update from DVT's compendium.")
	flag.Parse()

	if !*useradd && flag.NArg() > 0 {
		logger.Fatal("No argument besides usernames when adding users is accepted")
	}

	config, err := NewConfig(*config_path)
	if err != nil {
		logger.Fatal(err)
	}

	// Storage
	logger.Print("using database ", config.Database.Path)
	storage := NewStorage(config.Database)
	defer storage.Close()

	if *report {
		rf := NewReportFactory(storage, config.Report)
		report := rf.ReportWeek(rf.LastWeekCoordinates())
		if report.Len() == 0 {
			logger.Fatal("empty report")
		}
		fmt.Println(report)
		return
	}

	// reddit_bots

	// Reddit reddit_bot

	reddit_ok := true
	if config.Reddit.Username == "" || config.Reddit.Secret == "" || config.Reddit.Id == "" ||
		config.Reddit.Password == "" || config.Reddit.UserAgent == "" {
		fields := "id, secret, username, password, user_agent"
		msg := "Disabling reddit bot; at least one of the required fields of 'reddit' in the configuration file is empty"
		logger.Print(msg, ": ", fields)
		reddit_ok = false
	}

	var reddit_bot *RedditBot

	if reddit_ok {
		logger.Print("attempting to log reddit bot in")
		scanner, err := NewScanner(config.Reddit.RedditAuth, config.Reddit.UserAgent)
		if err != nil {
			logger.Fatal(err)
		}
		logger.Print("reddit bot successfully logged in")
		bot_logger := log.New(os.Stdout, "", logger_opts)
		reddit_bot = NewRedditBot(scanner, storage, bot_logger, config.Reddit.RedditBotConf)
	}

	// Command line registration
	if *useradd {
		if !reddit_ok {
			logger.Fatal("reddit bot must be running to register users")
		}
		usernames := flag.Args()
		for _, username := range usernames {
			hidden := strings.HasPrefix(username, config.HidePrefix)
			username = strings.TrimPrefix(username, config.HidePrefix)
			if res := reddit_bot.AddUser(username, hidden, true); res.Error != nil && !res.Exists {
				logger.Fatal(res.Error)
			}
		}
		return
	}
	// Launch reddit reddit_bot
	if reddit_ok {
		if *compendium {
			if err := reddit_bot.UpdateUsersFromCompendium(); err != nil {
				logger.Print(err)
			}
		}

		go reddit_bot.Run()

		compendium_interval := config.Reddit.CompendiumUpdateInterval.Value
		if compendium_interval != 0*time.Second {
			go func() {
				for {
					time.Sleep(compendium_interval)
					if err := reddit_bot.UpdateUsersFromCompendium(); err != nil {
						logger.Print(err)
					}
				}
			}()
		}
	}

	// Discord
	if config.Discord.DiscordBotConf.HidePrefix == "" {
		config.Discord.DiscordBotConf.HidePrefix = config.HidePrefix
	}
	discord_ok := config.Discord.Token != ""
	if !discord_ok {
		logger.Print("disabling discord bot; empty 'token' field in 'discord' section of the configuration file")
	}

	var discordbot *DiscordBot

	if discord_ok {
		logger.Print("attempting to connect discord bot")
		bot_logger := log.New(os.Stdout, "", logger_opts)
		discordbot, err = NewDiscordBot(storage, bot_logger, config.Discord.DiscordBotConf)
		if err != nil {
			logger.Fatal(err)
		}
		if err := discordbot.Run(); err != nil {
			logger.Fatal(err)
		}
		logger.Print("discord bot connected")
		defer discordbot.Close()
	}

	// Reddit reddit_bot <-> Discord reddit_bot
	if reddit_ok && discord_ok {
		logger.Print("connecting the discord bot and the reddit bot together")
		go reddit_bot.AddUserServer(discordbot.AddUser)

		reddit_evts := make(chan Comment)
		go discordbot.RedditEvents(reddit_evts)
		go reddit_bot.StreamSub("DownvoteTrolling", reddit_evts, config.Reddit.DVTInterval.Value)

		suspensions := reddit_bot.Suspensions()
		go discordbot.SignalSuspensions(suspensions)

		unsuspensions := reddit_bot.CheckUnsuspended(config.Reddit.UnsuspensionInterval.Value)
		go discordbot.SignalUnsuspensions(unsuspensions)

		if config.Discord.HighScores != "" {
			highscores := reddit_bot.StartHighScoresFeed(config.Discord.HighScoreThreshold)
			go discordbot.SignalHighScores(highscores)
		}
		logger.Print("discord bot and reddit bot connected")
	}

	// Web server for reports
	if config.Web.Listen != "" {
		logger.Print("lauching the web server")
		rf := NewReportFactory(storage, config.Report)
		wsrv := NewWebServer(config.Web.Listen, rf)
		go func() {
			err := wsrv.Run()
			if err != nil {
				logger.Print(err)
			}
		}()
		defer wsrv.Close()
	} else {
		logger.Print("disabling web server; empty 'listen' field in 'web' section of the configuration file")
	}

	logger.Print("all enabled components launched")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sig
}
