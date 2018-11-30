package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"time"
)

const Defaults string = `{
	"timezone": "UTC",
	"hide_prefix": "hide/",

	"database": {
		"backup_max_age": "24h",
		"backup_path": "./dab.db.backup",
		"cleanup_interval": "6h",
		"path": "./dab.db"
	},

	"discord": {
		"highscore_threshold": -1000,
		"prefix": "!"
	},

	"reddit": {
		"full_scan_interval": "6h",
		"inactivity_threshold": "2200h",
		"max_age": "24h",
		"max_batches": 5,
		"unsuspension_interval": "15m"
	},

	"report": {
		"cutoff": -50,
		"leeway": "12h",
		"nb_top": 5
	}
}`

type StorageConf struct {
	BackupMaxAge    Duration `json:"backup_max_age"`
	BackupPath      string   `json:"backup_path"`
	CleanupInterval Duration `json:"cleanup_interval"`
	Path            string   `json:"path"`
}

type RedditAuth struct {
	Id       string `json:"id"`
	Password string `json:"password"`
	Secret   string `json:"secret"`
	Username string `json:"username"`
}

type RedditScannerConf struct {
	FullScanInterval    Duration `json:"full_scan_interval"`
	HighScoreThreshold  int64    `json:"-"`
	InactivityThreshold Duration `json:"inactivity_threshold"`
	MaxAge              Duration `json:"max_age"`
	MaxBatches          uint     `json:"max_batches"`
}

type RedditUsersConf struct {
	CompendiumUpdateInterval Duration `json:"compendium_update_interval"`
	UnsuspensionInterval     Duration `json:"unsuspension_interval"`
}

type ReportConf struct {
	Cutoff   int64    `json:"cutoff"`
	Leeway   Duration `json:"leeway"`
	NbTop    uint     `json:"nb_top"`
	Timezone Timezone `json:"-"`
}

type DiscordBotConf struct {
	Admin      string   `json:"admin"`
	General    string   `json:"general"`
	HidePrefix string   `json:"hide_prefix"`
	HighScores string   `json:"highscores"`
	Log        string   `json:"log"`
	Prefix     string   `json:"prefix"`
	Token      string   `json:"token"`
	Welcome    string   `json:"welcome"`
	Timezone   Timezone `json:"-"`
}

type WebConf struct {
	Listen string `json:"listen"`
}

type Configuration struct {
	Timezone   Timezone `json:"timezone"`
	HidePrefix string   `json:"hide_prefix"`

	Database StorageConf

	Reddit struct {
		RedditAuth
		RedditScannerConf
		RedditUsersConf
		DVTInterval Duration `json:"dvt_interval"`
		UserAgent   string   `json:"user_agent"`
	}

	Report ReportConf

	Discord struct {
		DiscordBotConf
		HighScoreThreshold int64 `json:"highscore_threshold"`
	}

	Web WebConf
}

func NewConfiguration(path string) (Configuration, error) {
	var conf Configuration
	buffer := bytes.NewBuffer([]byte(Defaults))
	decoder := json.NewDecoder(buffer)
	decoder.DisallowUnknownFields()
	autopanic(decoder.Decode(&conf))

	raw_conf, err := ioutil.ReadFile(path)
	if err != nil {
		return conf, err
	}
	buffer.Write(raw_conf)
	if err := decoder.Decode(&conf); err != nil {
		return conf, err
	}

	if conf.HidePrefix == "" {
		panic("Prefix for 'hidden' users can't be an empty string")
	}

	conf.Report.Timezone = conf.Timezone
	conf.Discord.Timezone = conf.Timezone
	conf.Reddit.RedditScannerConf.HighScoreThreshold = conf.Discord.HighScoreThreshold
	if conf.Discord.DiscordBotConf.HidePrefix == "" {
		conf.Discord.DiscordBotConf.HidePrefix = conf.HidePrefix
	}

	return conf, nil
}

type Duration struct {
	Value time.Duration
}

func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return err
	}
	d.Value, err = time.ParseDuration(value)
	return err
}

type Timezone struct {
	Value *time.Location
}

func (tz *Timezone) UnmarshalJSON(raw []byte) error {
	var value string
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return err
	}
	tz.Value, err = time.LoadLocation(value)
	return err
}
