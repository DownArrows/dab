package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
		"max_batches": 5
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
	DiscordBotChannelsID
	Admin      string   `json:"admin"`
	HidePrefix string   `json:"hide_prefix"`
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
	if err := decoder.Decode(&conf); err != nil {
		return conf, err
	}

	raw_conf, err := ioutil.ReadFile(path)
	if err != nil {
		return conf, err
	}
	buffer.Write(raw_conf)
	if err := decoder.Decode(&conf); err != nil {
		return conf, err
	}

	conf.Report.Timezone = conf.Timezone
	conf.Discord.Timezone = conf.Timezone
	conf.Reddit.RedditScannerConf.HighScoreThreshold = conf.Discord.HighScoreThreshold
	if conf.Discord.DiscordBotConf.HidePrefix == "" {
		conf.Discord.DiscordBotConf.HidePrefix = conf.HidePrefix
	}

	return conf, nil
}

// Protect against values that are very likely to be mistakes
func (conf Configuration) HasSaneValues() error {
	if conf.HidePrefix == "" {
		return errors.New("prefix for hidden users can't be an empty string")
	} else if conf.Database.BackupMaxAge.Value < time.Hour {
		return errors.New("backup max age before renewal can't be less than an hour")
	} else if conf.Database.Path == conf.Database.BackupPath {
		return errors.New("backup path can't be the same as the database's path")
	} else if val := conf.Database.CleanupInterval.Value; val != 0 && val < time.Hour {
		return errors.New("interval between database cleanups can't be less than an hour")
	} else if val := conf.Reddit.CompendiumUpdateInterval.Value; val != 0 && val < time.Minute {
		return errors.New("interval between each check of the compendium can't be less than a minute if non-zero")
	} else if conf.Reddit.FullScanInterval.Value < time.Hour {
		return errors.New("interval for the full scan can't be less an hour")
	} else if conf.Reddit.InactivityThreshold.Value < 24*time.Hour {
		return errors.New("inactivity threshold can't be less than a day")
	} else if conf.Reddit.MaxAge.Value < 24*time.Hour {
		return errors.New("max comment age for further scanning can't be less than a day")
	} else if val := conf.Reddit.DVTInterval.Value; val != 0 && val < time.Minute {
		return errors.New("interval between each check of the downvote sub can't be less than a minute if non-zero")
	} else if conf.Reddit.HighScoreThreshold > -1 {
		return errors.New("high-score threshold can't be positive")
	} else if val := conf.Reddit.UnsuspensionInterval.Value; val != 0 && val < time.Minute {
		return errors.New("interval between batches of checks of suspended and deleted users can't be less than a minute if non-zero")
	} else if conf.Report.Leeway.Value < 0 {
		return errors.New("reports' leeway can't be negative")
	} else if conf.Report.Cutoff > 0 {
		return errors.New("reports' cut-off can't be higher than 0")
	}
	return nil
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
