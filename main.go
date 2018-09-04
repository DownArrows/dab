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
		"compendium_update_interval": "24h"
	},

	"report": {
		"timezone": "UTC",
		"leeway": "12h",
		"cutoff": -50,
		"max_length": 400000,
		"nb_top": 5
	},

	"discord": {
		"highscore_threshold": -1000
	}

}`

func main() {
	config_paths := []string{"/etc/dab.conf.json", "./dab.conf.json"}

	config := Config{}
	if err := json.Unmarshal([]byte(defaults), &config); err != nil {
		log.Fatal(err)
	}

	var err error
	var raw_config []byte
	for _, path := range config_paths {
		raw_config, err = ioutil.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.Fatal("Couldn't find config file at any of %v", config_paths)
	}

	if err := json.Unmarshal(raw_config, &config); err != nil {
		log.Fatal(err)
	}

	useradd := flag.String("useradd", "", "Add one or multiple comma-separated users to be tracked.")
	nodiscord := flag.Bool("nodiscord", false, "Don't connect to discord.")
	noreddit := flag.Bool("noreddit", false, "Don't connect to reddit.")
	report := flag.Bool("report", false, "Print the report for the last week.")
	flag.Parse()

	*nodiscord = *nodiscord || config.Discord.Token == ""

	// Storage
	db_path := config.Database.Path
	log.Print("Using database ", db_path)
	storage, err := NewStorage(db_path, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			time.Sleep(config.Database.CleanupInterval.Value)
			err := storage.Vacuum()
			if err != nil {
				log.Fatal(err)
			}
		}
	}()

	rt, err := NewReportTyper(storage, os.Stdout, config.Report)
	if err != nil {
		log.Fatal(err)
	}

	if *report {
		report, err := rt.ReportLastWeek()
		if err != nil {
			log.Fatal(err)
		}
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
		if err != nil {
			log.Fatal(err)
		}

		bot = NewBot(scanner, storage, os.Stdout, config.Scanner.BotConf)
	}

	// Command line registration
	if *useradd != "" {
		err = UserAdd(bot, *useradd)
		if err != nil {
			log.Fatal(err)
		}
		log.Print("Done")
		return
	}

	// Reddit bot
	if !*noreddit {
		go bot.Run()
		go func() {
			for {
				err := bot.UpdateUsersFromCompendium()
				if err != nil {
					log.Print(err)
				}
				time.Sleep(config.Scanner.CompendiumUpdateInterval.Value)
			}
		}()
	}

	// Discord bot
	if !*nodiscord {
		discordbot, err = NewDiscordBot(storage, os.Stdout, config.Discord.DiscordBotConf)
		if err != nil {
			log.Fatal(err)
		}

		go discordbot.Run()
	}

	// Reddit bot <-> Discord bot
	if !*nodiscord && !*noreddit {
		go bot.AddUserServer(discordbot.AddUser)

		reddit_evts := make(chan Comment)
		go discordbot.RedditEvents(reddit_evts)
		go bot.StreamSub("DownvoteTrolling", reddit_evts, time.Minute)

		suspensions := bot.Suspensions()
		go discordbot.SignalSuspensions(suspensions)

		unsuspensions := bot.CheckUnsuspended(config.Scanner.UnsuspensionInterval.Value)
		go discordbot.SignalUnsuspensions(unsuspensions)

		if config.Discord.HighScores != "" {
			highscores := bot.StartHighScoresFeed(config.Discord.HighScoreThreshold)
			go discordbot.SignalHighScores(highscores)
		}
	}

	wsrv := NewWebServer(rt)
	go func() {
		log.Fatal(wsrv.Run())
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sig
	log.Print("DAB stopped.")
}

func UserAdd(bot *Bot, arg string) error {
	usernames := strings.Split(arg, ",")
	for _, username := range usernames {
		res := bot.AddUser(username, false, true)
		if res.Error != nil {
			return res.Error
		}
	}
	log.Print("done")
	return nil
}
