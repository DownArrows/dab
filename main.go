package main

import (
	"flag"
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
	viper.SetDefault("report.timezone", "UTC")
	viper.SetDefault("report.leeway", time.Duration(12)*time.Hour)
	viper.SetDefault("report.cutoff", -50)
	viper.SetDefault("report.maxlength", 400000)

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal("Error reading config file: ", err)
	}

	db_path := viper.GetString("database.path")
	log.Print("Using database ", db_path)
	storage, err := NewStorage(db_path, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}

	scanner, err := NewRedditClient(RedditAuth{
		Id:       viper.GetString("client.id"),
		Key:      viper.GetString("client.secret"),
		Username: viper.GetString("client.username"),
		Password: viper.GetString("client.password"),
	})
	if err != nil {
		log.Fatal(err)
	}

	rt, err := NewReportTyper(
		storage,
		os.Stdout,
		viper.GetString("report.timezone"),
		viper.GetDuration("report.leeway"),
		viper.GetInt64("report.cutoff"),
		uint64(viper.GetInt64("report.maxlength")),
	)
	if err != nil {
		log.Fatal(err)
	}

	bot := NewBot(scanner, storage, os.Stdout, 24, 5)
	discordbot, err := NewDiscordBot(
		storage, os.Stdout,
		viper.GetString("discord.token"),
		viper.GetString("discord.general"),
		viper.GetString("discord.log"),
		viper.GetString("discord.admin"),
	)
	if err != nil {
		log.Fatal(err)
	}

	useradd := flag.String("useradd", "", "Add one or multiple comma-separated users to be tracked.")
	test := flag.Bool("test", false, "test")
	flag.Parse()

	if *test {
		batches, err := rt.ReportLastWeek()
		if err != nil {
			log.Fatal(err)
		}
		log.Print(batches)
		return
	}

	if *useradd != "" {
		err = UserAdd(bot, *useradd)
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	discord_kill := make(chan bool)
	go discordbot.Run(discord_kill)
	go func() {
		err = bot.Run()
		if err != nil {
			log.Fatal(err)
		}
	}()

	reddit_evts := make(chan Comment)
	go discordbot.RedditEvents(reddit_evts)
	go bot.StreamSub("DownvoteTrolling", reddit_evts)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sig
	discord_kill <- true
}

func UserAdd(bot *Bot, arg string) error {
	usernames := strings.Split(arg, ",")
	for _, username := range usernames {
		_, err := bot.AddUser(username, false)
		if err != nil {
			return err
		}
	}
	log.Print("done")
	return nil
}
