package main

import (
	"encoding/json"
	"io/ioutil"
	"time"
)

const Defaults string = `{
	"database": {
		"path": "./dab.db",
		"cleanup_interval": "6h"
	},

	"hide_prefix": "hide/",

	"reddit": {
		"max_batches": 5,
		"max_age": "24h",
		"unsuspension_interval": "15m",
		"inactivity_threshold": "2200h",
		"full_scan_interval": "6h",
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

type StorageConf struct {
	Path            string   `json:"path"`
	CleanupInterval Duration `json:"cleanup_interval"`
}

type RedditAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Id       string `json:"id"`
	Secret   string `json:"secret"`
}

type RedditBotConf struct {
	MaxAge              Duration `json:"max_age"`
	MaxBatches          uint     `json:"max_batches"`
	InactivityThreshold Duration `json:"inactivity_threshold"`
	FullScanInterval    Duration `json:"full_scan_interval"`
}

type ReportConf struct {
	Leeway    Duration `json:"leeway"`
	Timezone  Timezone `json:"timezone"`
	Cutoff    int64    `json:"cutoff"`
	MaxLength uint64   `json:"max_length"`
	NbTop     uint     `json:"nb_top"`
}

type DiscordBotConf struct {
	Token      string `json:"token"`
	General    string `json:"general"`
	Log        string `json:"log"`
	HighScores string `json:"highscores"`
	Admin      string `json:"admin"`
	Prefix     string `json:"prefix"`
	Welcome    string `json:"welcome"`
	HidePrefix string `json:"hide_prefix"`
}

type Configuration struct {
	Database StorageConf

	HidePrefix string `json:"hide_prefix"`

	Reddit struct {
		RedditAuth
		RedditBotConf
		UserAgent                string   `json:"user_agent"`
		UnsuspensionInterval     Duration `json:"unsuspension_interval"`
		CompendiumUpdateInterval Duration `json:"compendium_update_interval"`
		DVTInterval              Duration `json:"dvt_interval"`
	}

	Report ReportConf

	Discord struct {
		DiscordBotConf
		HighScoreThreshold int64 `json:"highscore_threshold"`
	}

	Web struct {
		Listen string `json:"listen"`
	}
}

func NewConfiguration(path string) (Configuration, error) {
	var conf Configuration

	autopanic(json.Unmarshal([]byte(Defaults), &conf))

	raw_conf, err := ioutil.ReadFile(path)
	if err != nil {
		return conf, err
	}

	if err := json.Unmarshal(raw_conf, &conf); err != nil {
		return conf, err
	}

	if conf.HidePrefix == "" {
		panic("Prefix for 'hidden' users can't be an empty string")
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
