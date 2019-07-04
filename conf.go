package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"time"
)

const Defaults string = `{
	"timezone": "UTC",
	"hide_prefix": "hide/",

	"database": {
		"backup_max_age": "24h",
		"backup_path": "./dab.db.backup",
		"cleanup_interval": "30m",
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
	CutOff   int64    `json:"cutoff"`
	Leeway   Duration `json:"leeway"`
	NbTop    uint     `json:"nb_top"`
	Timezone Timezone `json:"-"`
}

type DiscordBotConf struct {
	DiscordBotChannelsID
	HidePrefix     string   `json:"hide_prefix"`
	Prefix         string   `json:"prefix"`
	PrivilegedRole string   `json:"privileged_role"`
	Timezone       Timezone `json:"-"`
	Token          string   `json:"token"`
	Welcome        string   `json:"welcome"`
}

type DiscordBotChannelsID struct {
	General    string `json:"general"`
	Log        string `json:"log"`
	HighScores string `json:"highscores"`
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
		Admin              string `json:"admin"` // Deprecated
		HighScoreThreshold int64  `json:"highscore_threshold"`
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
	} else if val := conf.Database.CleanupInterval.Value; val != 0 && val < time.Minute {
		return errors.New("interval between database cleanups can't be less than a minute")
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
	} else if conf.Report.CutOff > 0 {
		return errors.New("reports' cut-off can't be higher than 0")
	}
	return nil
}

func (conf Configuration) Components() ComponentsConf {
	c := ComponentsConf{}

	reddit_required := map[string]string{
		"id":         conf.Reddit.Id,
		"password":   conf.Reddit.Password,
		"secret":     conf.Reddit.Secret,
		"user agent": conf.Reddit.UserAgent,
		"username":   conf.Reddit.Username,
	}
	var reddit_invalid []string
	for name, value := range reddit_required {
		if value == "" {
			reddit_invalid = append(reddit_invalid, name)
		}
	}

	c.Reddit.Name = "reddit"
	if len(reddit_invalid) > 0 {
		c.Reddit.Error = errors.New("missing required fields: " + strings.Join(reddit_invalid, ", "))
	} else {
		c.Reddit.Enabled = true
	}

	c.Discord.Name = "discord"
	if conf.Discord.Token == "" {
		c.Discord.Error = errors.New("empty token")
	} else {
		c.Discord.Enabled = true
	}

	c.Web.Name = "web server"
	if conf.Web.Listen == "" {
		c.Web.Error = errors.New("missing or empty listen specification")
	} else {
		c.Web.Enabled = true
	}

	return c
}

type componentConf struct {
	Enabled bool
	Name    string
	Error   error
}

func (c componentConf) String() string {
	if c.Enabled {
		return fmt.Sprintf("%s: enabled", c.Name)
	}
	return fmt.Sprintf("%s: %s", c.Name, c.Error)
}

type ComponentsConf struct {
	Discord componentConf
	Reddit  componentConf // subsumes several components related to reddit
	Web     componentConf
}

func (c ComponentsConf) String() string {
	return fmt.Sprintf("%s; %s; %s", c.Discord, c.Reddit, c.Web)
}

// Needed to decode JSON strings to time.Duration.
type Duration struct {
	Value time.Duration
}

// Called by the JSON decoder.
func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return err
	}
	d.Value, err = time.ParseDuration(value)
	return err
}

// Needed to decode JSON strings to *time.Location.
type Timezone struct {
	Value *time.Location
}

// Called by the JSON decoder.
func (tz *Timezone) UnmarshalJSON(raw []byte) error {
	var value string
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return err
	}
	tz.Value, err = time.LoadLocation(value)
	return err
}
