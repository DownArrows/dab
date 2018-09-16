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

	"scanner": {
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
	logger := log.New(os.Stdout, "", log.Lshortfile)

	fatal := func(err error) {
		if err != nil {
			logger.Fatal(err)
		}
	}

	defer logger.Print("DAB stopped.")

	config_paths := []string{"/etc/dab.conf.json", "./dab.conf.json"}

	config := Config{}
	fatal(json.Unmarshal([]byte(defaults), &config))

	var err error
	var raw_config []byte
	for _, path := range config_paths {
		raw_config, err = ioutil.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		logger.Fatalf("Couldn't find config file at any of %v", config_paths)
	}

	fatal(json.Unmarshal(raw_config, &config))

	useradd := flag.String("useradd", "", "Add one or multiple comma-separated users to be tracked.")
	nodiscord := flag.Bool("nodiscord", false, "Don't connect to discord.")
	noreddit := flag.Bool("noreddit", false, "Don't connect to reddit.")
	report := flag.Bool("report", false, "Print the report for the last week.")
	flag.Parse()

	*nodiscord = *nodiscord || config.Discord.Token == ""

	// Storage
	db_path := config.Database.Path
	logger.Print("Using database ", db_path)
	storage, err := NewStorage(db_path)
	fatal(err)
	defer storage.Close()

	go func() {
		for {
			time.Sleep(config.Database.CleanupInterval.Value)
			fatal(storage.Vacuum())
		}
	}()

	rt, err := NewReportTyper(storage, config.Report)
	fatal(err)

	if *report {
		report, err := rt.ReportLastWeek()
		fatal(err)
		for _, chunk := range report {
			fmt.Println(chunk)
		}
		return
	}

	// Bots
	var bot *Bot
	var discordbot *DiscordBot

	// Reddit bot or new users registration from the command line
	if !*noreddit || *useradd != "" {
		scanner, err := NewRedditClient(config.Scanner.RedditAuth, config.Scanner.UserAgent)
		fatal(err)
		logger := log.New(os.Stdout, "", log.Lshortfile)
		bot = NewBot(scanner, storage, logger, config.Scanner.BotConf)
	}

	// Command line registration
	if *useradd != "" {
		fatal(UserAdd(logger, bot, *useradd))
		logger.Print("Done")
		return
	}

	// Reddit bot
	if !*noreddit {
		go bot.Run()
		go func() {
			for {
				if err := bot.UpdateUsersFromCompendium(); err != nil {
					logger.Print(err)
				}
				time.Sleep(config.Scanner.CompendiumUpdateInterval.Value)
			}
		}()
	}

	// Discord bot
	if !*nodiscord {
		logger := log.New(os.Stdout, "", log.Lshortfile)
		discordbot, err = NewDiscordBot(storage, logger, config.Discord.DiscordBotConf)
		fatal(err)
		fatal(discordbot.Run())
		defer discordbot.Close()
	}

	// Reddit bot <-> Discord bot
	if !*nodiscord && !*noreddit {
		go bot.AddUserServer(discordbot.AddUser)

		reddit_evts := make(chan Comment)
		go discordbot.RedditEvents(reddit_evts)
		go bot.StreamSub("DownvoteTrolling", reddit_evts, config.Scanner.DVTInterval.Value)

		suspensions := bot.Suspensions()
		go discordbot.SignalSuspensions(suspensions)

		unsuspensions := bot.CheckUnsuspended(config.Scanner.UnsuspensionInterval.Value)
		go discordbot.SignalUnsuspensions(unsuspensions)

		if config.Discord.HighScores != "" {
			highscores := bot.StartHighScoresFeed(config.Discord.HighScoreThreshold)
			go discordbot.SignalHighScores(highscores)
		}
	}

	if config.Web.Listen != "" {
		wsrv := NewWebServer(config.Web.Listen, rt)
		go func() {
			err := wsrv.Run()
			if err != nil {
				logger.Print(err)
			}
		}()
		defer wsrv.Close()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sig
}

func UserAdd(logger *log.Logger, bot *Bot, arg string) error {
	usernames := strings.Split(arg, ",")
	for _, username := range usernames {
		res := bot.AddUser(username, false, true)
		if res.Error != nil {
			return res.Error
		}
	}
	logger.Print("Done")
	return nil
}
