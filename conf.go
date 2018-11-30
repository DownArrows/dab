package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"time"
)

const Defaults string = `{
	"timezone": "UTC",

	"database": {
		"path": "./dab.db",
		"cleanup_interval": "6h",
		"backup_path": "./dab.db.backup",
		"backup_max_age": "24h"
	},

	"hide_prefix": "hide/",

	"reddit": {
		"max_batches": 5,
		"max_age": "24h",
		"unsuspension_interval": "15m",
		"inactivity_threshold": "2200h",
		"full_scan_interval": "6h"
	},

	"report": {
		"leeway": "12h",
		"cutoff": -50,
		"nb_top": 5
	},

	"discord": {
		"highscore_threshold": -1000,
		"prefix": "!"
	}
}`

type StorageConf struct {
	Path            string   `json:"path"`
	CleanupInterval Duration `json:"cleanup_interval"`
	BackupPath      string   `json:"backup_path"`
	BackupMaxAge    Duration `json:"backup_max_age"`
}

type RedditAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Id       string `json:"id"`
	Secret   string `json:"secret"`
}

type RedditScannerConf struct {
	MaxAge              Duration `json:"max_age"`
	MaxBatches          uint     `json:"max_batches"`
	InactivityThreshold Duration `json:"inactivity_threshold"`
	FullScanInterval    Duration `json:"full_scan_interval"`
	HighScoreThreshold  int64    `json:"-"`
}

type RedditUsersConf struct {
	UnsuspensionInterval     Duration `json:"unsuspension_interval"`
	CompendiumUpdateInterval Duration `json:"compendium_update_interval"`
}

type ReportConf struct {
	Leeway   Duration `json:"leeway"`
	Timezone Timezone `json:"-"`
	Cutoff   int64    `json:"cutoff"`
	NbTop    uint     `json:"nb_top"`
}

type DiscordBotConf struct {
	Token      string   `json:"token"`
	General    string   `json:"general"`
	Log        string   `json:"log"`
	HighScores string   `json:"highscores"`
	Admin      string   `json:"admin"`
	Prefix     string   `json:"prefix"`
	Welcome    string   `json:"welcome"`
	HidePrefix string   `json:"hide_prefix"`
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
