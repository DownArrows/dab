package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const defaults string = `{
	"database": {
		"path": "./dab.db",
		"cleanup_interval": "1h"
	},

	"reddit": {
		"max_batches": 5,
		"max_age": "24h",
		"unsuspension_interval": "15m",
		"inactivity_threshold": "2200h",
		"full_scan_interval": "6h",
		"compendium_update_interval": "24h",
		"dvt_interval": "5m"
	},

	"report": {
		"timezone": "UTC",
		"leeway": "12h",
		"cutoff": -50,
		"max_length": 400000,
		"nb_top": 5
	},

	"discord": {
		"highscore_threshold": -1000,
		"prefix": "!"
	}

}`

func main() {
	logger := log.New(os.Stderr, "", log.Lshortfile)

	defer logger.Print("DAB stopped.")

	config := Config{}
	if err := json.Unmarshal([]byte(defaults), &config); err != nil {
		logger.Fatal(err)
	}

	report := flag.Bool("report", false, "Print the report for the last week.")
	useradd := flag.String("useradd", "", "Add one or multiple comma-separated usernames to be tracked.")
	config_path := flag.String("config", "./dab.conf.json", "Path to the configuration file.")
	flag.Parse()

	raw_config, err := ioutil.ReadFile(*config_path)
	if err != nil {
		logger.Fatal(err)
	}
	if err := json.Unmarshal(raw_config, &config); err != nil {
		logger.Fatal(err)
	}

	// Storage
	db_path := config.Database.Path
	logger.Print("Using database ", db_path)
	storage, err := NewStorage(db_path)
	if err != nil {
		logger.Fatal(err)
	}
	defer storage.Close()

	go func() {
		for {
			time.Sleep(config.Database.CleanupInterval.Value)
			if err := storage.Vacuum(); err != nil {
				logger.Fatal(err)
			}
		}
	}()

	if *report {
		rt, err := NewReportTyper(storage, config.Report)
		if err != nil {
			logger.Fatal(err)
		}
		report, err := rt.ReportLastWeek()
		if err != nil {
			logger.Fatal(err)
		}
		for _, chunk := range report {
			fmt.Println(chunk)
		}
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
		scanner, err := NewScanner(config.Reddit.RedditAuth, config.Reddit.UserAgent)
		if err != nil {
			logger.Fatal(err)
		}
		logger := log.New(os.Stdout, "", log.Lshortfile)
		reddit_bot = NewRedditBot(scanner, storage, logger, config.Reddit.RedditBotConf)
	}

	// Command line registration
	if *useradd != "" {
		if !reddit_ok {
			logger.Fatal("Reddit bot must be running to register users")
		}
		usernames := strings.Split(*useradd, ",")
		fmt.Println(usernames)
		for _, username := range usernames {
			if res := reddit_bot.AddUser(username, false, true); res.Error != nil && !res.Exists {
				logger.Fatal(res.Error)
			}
		}
		return
	}

	// Launch reddit reddit_bot
	if reddit_ok {
		go reddit_bot.Run()
		go func() {
			for {
				if err := reddit_bot.UpdateUsersFromCompendium(); err != nil {
					logger.Print(err)
				}
				time.Sleep(config.Reddit.CompendiumUpdateInterval.Value)
			}
		}()
	}

	// Discord

	discord_ok := config.Discord.Token != ""
	if !discord_ok {
		logger.Print("Disabling discord bot; empty 'token' field in 'discord' section of the configuration file")
	}

	var discordbot *DiscordBot

	if discord_ok {
		logger := log.New(os.Stdout, "", log.Lshortfile)
		discordbot, err = NewDiscordBot(storage, logger, config.Discord.DiscordBotConf)
		if err != nil {
			logger.Fatal(err)
		}
		if err := discordbot.Run(); err != nil {
			logger.Fatal(err)
		}
		defer discordbot.Close()
	}

	// Reddit reddit_bot <-> Discord reddit_bot
	if reddit_ok && discord_ok {
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
	}

	// Web server for reports
	if config.Web.Listen != "" {
		rt, err := NewReportTyper(storage, config.Report)
		if err != nil {
			logger.Fatal(err)
		}
		wsrv := NewWebServer(config.Web.Listen, rt)
		go func() {
			err := wsrv.Run()
			if err != nil {
				logger.Print(err)
			}
		}()
		defer wsrv.Close()
	} else {
		logger.Print("Disabling web server; empty 'listen' field in 'web' section of the configuration file")
	}

	logger.Print("All enabled components launched")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sig
}
