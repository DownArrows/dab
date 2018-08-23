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
	viper.SetDefault("discord.highscores", "")

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

	rt, err := NewReportTyper(
		storage,
		os.Stdout,
		viper.GetString("report.timezone"),
		viper.GetDuration("report.leeway"),
		viper.GetInt64("report.cutoff"),
		uint64(viper.GetInt64("report.maxlength")),
		viper.GetInt("report.nb_top"),
	)
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

		bot = NewBot(
			scanner, storage, os.Stdout,
			viper.GetDuration("scanner.max_age"),
			viper.GetInt("scanner.max_batches"),
		)
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
	}

	// Discord bot
	if !*nodiscord {
		discordbot, err = NewDiscordBot(
			storage, os.Stdout,
			viper.GetString("discord.token"),
			viper.GetString("discord.general"),
			viper.GetString("discord.log"),
			viper.GetString("discord.highscores"),
			viper.GetString("discord.admin"),
		)
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
