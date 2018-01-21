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
	viper.SetDefault("database.cleanup_interval", time.Hour)
	viper.SetDefault("report.timezone", "UTC")
	viper.SetDefault("report.leeway", time.Duration(12)*time.Hour)
	viper.SetDefault("report.cutoff", -50)
	viper.SetDefault("report.maxlength", 400000)

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal("Error reading config file: ", err)
	}

	useradd := flag.String("useradd", "", "Add one or multiple comma-separated users to be tracked.")
	nodiscord := flag.Bool("nodiscord", false, "Don't connect to discord.")
	noreddit := flag.Bool("noreddit", false, "Don't connect to reddit.")
	flag.Parse()

	// Storage
	db_path := viper.GetString("database.path")
	log.Print("Using database ", db_path)
	storage, err := NewStorage(db_path, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		time.Sleep(viper.GetDuration("database.cleanup_interval"))
		storage.Vacuum()
	}()

	// Bots
	var bot *Bot
	var discordbot *DiscordBot

	// Reddit bot or new users registration from the command line
	if !*noreddit || *useradd != "" {
		scanner, err := NewRedditClient(RedditAuth{
			Id:       viper.GetString("client.id"),
			Key:      viper.GetString("client.secret"),
			Username: viper.GetString("client.username"),
			Password: viper.GetString("client.password"),
		})
		if err != nil {
			log.Fatal(err)
		}

		bot = NewBot(scanner, storage, os.Stdout, 24, 5)
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
		//		rt, err := NewReportTyper(
		//			storage,
		//			os.Stdout,
		//			viper.GetString("report.timezone"),
		//			viper.GetDuration("report.leeway"),
		//			viper.GetInt64("report.cutoff"),
		//			uint64(viper.GetInt64("report.maxlength")),
		//		)
		//		if err != nil {
		//			log.Fatal(err)
		//		}

		go bot.Run()
	}

	// Discord bot
	if !*nodiscord {
		discordbot, err = NewDiscordBot(
			storage, os.Stdout,
			viper.GetString("discord.token"),
			viper.GetString("discord.general"),
			viper.GetString("discord.log"),
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
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sig
	log.Print("DAB stopped.")
}

func stream(config *viper.Viper, bot *Bot, discordbot *DiscordBot) {
	subs := config.AllKeys()
	if len(subs) > 0 {
		reddit_evts := make(chan Comment)
		go discordbot.RedditEvents(reddit_evts)
		for _, sub := range subs {
			sleep := config.GetDuration(sub)
			go bot.StreamSub(sub, reddit_evts, sleep)
		}
	}
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
