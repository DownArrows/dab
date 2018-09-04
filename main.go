package main

import (
	"flag"
	"fmt"
	"github.com/spf13/viper"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {

	viper.SetConfigName("dab")
	viper.AddConfigPath("/etc/")
	viper.AddConfigPath("$HOME/.config/")
	viper.AddConfigPath(".")

	viper.SetDefault("database.path", "./dab.db")
	viper.SetDefault("database.cleanup_interval", time.Hour)
	viper.SetDefault("report.timezone", "UTC")
	viper.SetDefault("report.leeway", 12*time.Hour)
	viper.SetDefault("report.cutoff", -50)
	viper.SetDefault("report.max_length", 400000)
	viper.SetDefault("report.nb_top", 5)
	viper.SetDefault("scanner.max_batches", 5)
	viper.SetDefault("scanner.max_age", 24*time.Hour)
	viper.SetDefault("scanner.unsuspension_interval", 15*time.Minute)
	viper.SetDefault("scanner.inactivity_threshold", 2200*time.Hour)
	viper.SetDefault("scanner.full_scan_interval", 6*time.Hour)
	viper.SetDefault("discord.highscores", "")
	viper.SetDefault("scanner.compendium_update_interval", 24*time.Hour)

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal("Error reading config file: ", err)
	}

	useradd := flag.String("useradd", "", "Add one or multiple comma-separated users to be tracked.")
	nodiscord := flag.Bool("nodiscord", false, "Don't connect to discord.")
	noreddit := flag.Bool("noreddit", false, "Don't connect to reddit.")
	report := flag.Bool("report", false, "Print the report for the last week.")
	flag.Parse()

	// Storage
	db_path := viper.GetString("database.path")
	log.Print("Using database ", db_path)
	storage, err := NewStorage(db_path, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			time.Sleep(viper.GetDuration("database.cleanup_interval"))
			err := storage.Vacuum()
			if err != nil {
				log.Fatal(err)
			}
		}
	}()

	timezone, err := time.LoadLocation(viper.GetString("report.timezone"))
	if err != nil {
		log.Fatal(err)
	}
	rt, err := NewReportTyper(storage, os.Stdout, ReportConf{
		Timezone:  timezone,
		Leeway:    viper.GetDuration("report.leeway"),
		Cutoff:    viper.GetInt64("report.cutoff"),
		MaxLength: uint64(viper.GetInt64("report.maxlength")),
		NbTop:     viper.GetInt("report.nb_top"),
	})
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
		auth := RedditAuth{
			Id:       viper.GetString("scanner.id"),
			Key:      viper.GetString("scanner.secret"),
			Username: viper.GetString("scanner.username"),
			Password: viper.GetString("scanner.password"),
		}
		ua := viper.GetString("scanner.user_agent")

		scanner, err := NewRedditClient(auth, ua)
		if err != nil {
			log.Fatal(err)
		}

		bot = NewBot(scanner, storage, os.Stdout, BotConf{
			MaxAge:              viper.GetDuration("scanner.max_age"),
			MaxBatches:          uint(viper.GetInt("scanner.max_batches")),
			InactivityThreshold: viper.GetDuration("scanner.inactivity_threshold"),
			FullScanInterval:    viper.GetDuration("scanner.full_scan_interval"),
		})
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
			interval := viper.GetDuration("scanner.compendium_update_interval")
			for {
				err := bot.UpdateUsersFromCompendium()
				if err != nil {
					log.Print(err)
				}
				time.Sleep(interval)
			}
		}()
	}

	// Discord bot
	if !*nodiscord {
		discordbot, err = NewDiscordBot(storage, os.Stdout, DiscordBotConf{
			Token:      viper.GetString("discord.token"),
			General:    viper.GetString("discord.general"),
			Log:        viper.GetString("discord.log"),
			HighScores: viper.GetString("discord.highscores"),
			Admin:      viper.GetString("discord.admin"),
		})
		if err != nil {
			log.Fatal(err)
		}

		go discordbot.Run()
	}

	// Reddit bot <-> Discord bot
	if !*nodiscord && !*noreddit {
		go bot.AddUserServer(discordbot.AddUser)
		stream(viper.Sub("new"), bot, discordbot)

		suspensions := bot.Suspensions()
		go discordbot.SignalSuspensions(suspensions)

		interval := viper.GetDuration("scanner.unsuspension_interval")
		unsuspensions := bot.CheckUnsuspended(interval)
		go discordbot.SignalUnsuspensions(unsuspensions)

		if viper.IsSet("discord.highscore_threshold") {
			threshold := viper.GetInt64("discord.highscore_threshold")
			highscores := bot.StartHighScoresFeed(threshold)
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

func stream(config *viper.Viper, bot *Bot, discordbot *DiscordBot) {
	if config == nil {
		return
	}
	reddit_evts := make(chan Comment)
	go discordbot.RedditEvents(reddit_evts)
	for _, sub := range config.AllKeys() {
		sleep := config.GetDuration(sub)
		go bot.StreamSub(sub, reddit_evts, sleep)
	}
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
