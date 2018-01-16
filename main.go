package main

import (
	"flag"
	"github.com/spf13/viper"
	"log"
	"os"
	"strings"
)

func main() {

	viper.SetConfigName("dab")
	viper.AddConfigPath("/etc/")
	viper.AddConfigPath("$HOME/.config/")
	viper.AddConfigPath(".")

	viper.SetDefault("database.path", "./dab.db")

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

	bot := NewBot(scanner, storage, os.Stdout, 24, 5)

	useradd := flag.String("useradd", "", "Add one or multiple comma-separated users to be tracked.")
	flag.Parse()
	if *useradd != "" {
		err = UserAdd(bot, *useradd)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		err = bot.Run()
		if err != nil {
			log.Fatal(err)
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
