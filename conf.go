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

// Defaults defines the default configuration of the whole application.
const Defaults string = `{
	"timezone": "UTC",
	"hide_prefix": "hide/",

	"database": {
		"backup_max_age": "24h",
		"backup_path": "./dab.db.backup",
		"cleanup_interval": "30m",
		"path": "./dab.db",
		"retry_connection": {
			"times": 25,
			"max_interval": "10s",
			"reset_after": "5m"
		},
		"timeout": "15s"
	},

	"discord": {
		"retry_connection": {
			"times": 5,
			"max_interval": "2m",
			"reset_after": "2h"
		},
		"highscore_threshold": -1000,
		"prefix": "!"
	},

	"reddit": {
		"retry_connection": {
			"times": 10,
			"max_interval": "5m",
			"reset_after": "1h"
		},
		"full_scan_interval": "6h",
		"inactivity_threshold": "2200h",
		"max_age": "24h",
		"max_batches": 5
	},

	"report": {
		"cutoff": -50,
		"nb_top": 5
	},

	"web": {
		"default_limit": 100,
		"dirty_reads": true,
		"max_limit": 1000,
		"nb_db_conn": 10
	}
}`

// StorageConf describes the configuration of the Storage layer.
type StorageConf struct {
	BackupMaxAge    Duration  `json:"backup_max_age"`
	BackupPath      string    `json:"backup_path"`
	CleanupInterval Duration  `json:"cleanup_interval"`
	Path            string    `json:"path"`
	Retry           RetryConf `json:"retry_connection"`
	Timeout         Duration  `json:"timeout"`
}

// RetryConf describes the configuration of the retry logic for a component.
type RetryConf struct {
	MaxInterval Duration `json:"max_interval"`
	ResetAfter  Duration `json:"reset_after"`
	Times       int      `json:"times"`
}

// RedditAuth describes the authentication credentials for Reddit.
type RedditAuth struct {
	ID       string `json:"id"`
	Password string `json:"password"`
	Secret   string `json:"secret"`
	Username string `json:"username"`
}

// RedditScannerConf describes the configuration of the scanner for Reddit.
type RedditScannerConf struct {
	FullScanInterval    Duration `json:"full_scan_interval"`
	HighScoreThreshold  int64    `json:"-"`
	InactivityThreshold Duration `json:"inactivity_threshold"`
	MaxAge              Duration `json:"max_age"`
	MaxBatches          uint     `json:"max_batches"`
}

// WatchSubmissions describes the configuration for watching submissions to a subreddit (deprecated).
type WatchSubmissions struct {
	Target   string
	Interval Duration
}

// RedditUsersConf describes the configuration for the component that manages users.
type RedditUsersConf struct {
	Compendium               WatchCompendiumConf `json:"compendium"`
	CompendiumUpdateInterval Duration            `json:"compendium_update_interval"` // Deprecated
	ResurrectionsInterval    Duration            `json:"resurrections_interval"`
	UnsuspensionInterval     Duration            `json:"unsuspension_interval"` // Deprecated
}

// WatchCompendiumConf describes the configuration for watching the users added to a third-party compendium (deprecated).
type WatchCompendiumConf struct {
	Sub            string   `json:"sub"`             // Deprecated
	UpdateInterval Duration `json:"update_interval"` // Deprecated
}

// CompendiumConf describes the configuration for the compendium generated by the application.
type CompendiumConf struct {
	NbTop    uint     `json:"nb_top"`
	Timezone Timezone `json:"-"`
}

// ReportConf describes the configuration for generating reports, which is propagated to the configuration of the compendium.
type ReportConf struct {
	CutOff   int64    `json:"cutoff"`
	Leeway   Duration `json:"leeway"` // Deprecated
	NbTop    uint     `json:"nb_top"`
	Timezone Timezone `json:"-"`
}

// DiscordBotConf describes the configuration for the bot for Discord.
type DiscordBotConf struct {
	DiscordBotChannelsID
	HidePrefix     string   `json:"hide_prefix"`
	Prefix         string   `json:"prefix"`
	PrivilegedRole string   `json:"privileged_role"`
	Timezone       Timezone `json:"-"`
	Token          string   `json:"token"`
	Welcome        string   `json:"welcome"`
}

// DiscordBotChannelsID describes the channels used by the Discord bot.
type DiscordBotChannelsID struct {
	General    string `json:"general"`
	Graveyard  string `json:"graveyard"`
	HighScores string `json:"highscores"`
	Log        string `json:"log"`
}

// WebConf describes the configuration for the application's web server.
type WebConf struct {
	DefaultLimit uint   `json:"default_limit"`
	DirtyReads   bool   `json:"dirty_reads"`
	Listen       string `json:"listen"`
	MaxLimit     uint   `json:"max_limit"`
	NbDBConn     uint   `json:"nb_db_conn"`
	RootDir      string `json:"root_dir"`
}

// Configuration holds the configuration for the whole application.
type Configuration struct {
	Timezone   Timezone `json:"timezone"`
	HidePrefix string   `json:"hide_prefix"`

	Database StorageConf

	Reddit struct {
		RedditAuth
		RedditScannerConf
		RedditUsersConf
		DVTInterval      Duration           `json:"dvt_interval"` // Deprecated
		Retry            RetryConf          `json:"retry_connection"`
		UserAgent        string             `json:"user_agent"`
		WatchSubmissions []WatchSubmissions `json:"watch_submissions"` // Deprecated
	}

	Compendium CompendiumConf `json:"-"`
	Report     ReportConf

	Discord struct {
		DiscordBotConf
		Admin              string    `json:"admin"` // Deprecated
		HighScoreThreshold int64     `json:"highscore_threshold"`
		Retry              RetryConf `json:"retry_connection"`
	}

	Web WebConf
}

// NewConfiguration returns the configuration for the whole application by reading a JSON file given as a path.
func NewConfiguration(path string) (Configuration, error) {
	var conf Configuration
	buffer := bytes.NewBuffer([]byte(Defaults))
	decoder := json.NewDecoder(buffer)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&conf); err != nil {
		return conf, err
	}

	rawConf, err := ioutil.ReadFile(path)
	if err != nil {
		return conf, err
	}
	buffer.Write(rawConf)
	if err := decoder.Decode(&conf); err != nil {
		return conf, err
	}

	conf.Report.Timezone = conf.Timezone
	conf.Compendium.Timezone = conf.Timezone
	conf.Discord.Timezone = conf.Timezone

	conf.Compendium.NbTop = conf.Report.NbTop

	conf.Reddit.RedditScannerConf.HighScoreThreshold = conf.Discord.HighScoreThreshold
	if conf.Discord.DiscordBotConf.HidePrefix == "" {
		conf.Discord.DiscordBotConf.HidePrefix = conf.HidePrefix
	}

	if conf.Discord.DiscordBotConf.Graveyard == "" {
		conf.Discord.DiscordBotConf.Graveyard = conf.Discord.DiscordBotConf.General
	}

	if conf.Reddit.UnsuspensionInterval.Value != 0 {
		conf.Reddit.ResurrectionsInterval.Value = conf.Reddit.UnsuspensionInterval.Value
	}

	return conf, nil
}

// HasSaneValues protect against values that are very likely to be mistakes
func (conf Configuration) HasSaneValues() error {
	if conf.HidePrefix == "" {
		return errors.New("prefix for hidden users can't be an empty string")
	} else if conf.Database.BackupMaxAge.Value < time.Hour {
		return errors.New("backup max age before renewal can't be less than an hour")
	} else if conf.Database.Path == conf.Database.BackupPath {
		return errors.New("backup path can't be the same as the database's path")
	} else if val := conf.Database.CleanupInterval.Value; val != 0 && val < time.Minute {
		return errors.New("interval between database cleanups can't be less than a minute")
	} else if conf.Reddit.FullScanInterval.Value < time.Hour {
		return errors.New("interval for the full scan can't be less an hour")
	} else if conf.Reddit.InactivityThreshold.Value < 24*time.Hour {
		return errors.New("inactivity threshold can't be less than a day")
	} else if conf.Reddit.MaxAge.Value < 24*time.Hour {
		return errors.New("max comment age for further scanning can't be less than a day")
	} else if conf.Reddit.HighScoreThreshold > -1 {
		return errors.New("high-score threshold can't be positive")
	} else if val := conf.Reddit.ResurrectionsInterval.Value; val != 0 && val < time.Minute {
		return errors.New("interval between batches of checks of resurrections of users can't be less than a minute if non-zero")
	} else if conf.Report.Leeway.Value < 0 { // Deprecated
		return errors.New("reports' leeway can't be negative")
	} else if conf.Report.CutOff > 0 {
		return errors.New("reports' cut-off can't be higher than 0")
	} else if conf.Web.NbDBConn == 0 {
		return errors.New("the number of database connections from the web server can't be 0")
	}
	return nil
}

// Deprecations returns a slice of deprecation messages.
func (conf Configuration) Deprecations() []string {
	var msgs []string

	if conf.Discord.Admin != "" {
		msgs = append(msgs, "discord.admin is deprecated, use discord.privileged_role instead")
	}

	if conf.Reddit.DVTInterval.Value != 0 {
		msgs = append(msgs, "reddit.dvt_interval is deprecated")
	}

	if conf.Reddit.WatchSubmissions != nil {
		msgs = append(msgs, "reddit.watch_submissions is deprecated")
	}

	if conf.Reddit.CompendiumUpdateInterval.Value != 0 {
		msgs = append(msgs, "reddit.compendium_update_interval is deprecated")
	}

	emptyCompendiumConf := WatchCompendiumConf{}
	if conf.Reddit.Compendium != emptyCompendiumConf {
		msgs = append(msgs, "reddit.compendium is deprecated")
	}

	if conf.Report.Leeway.Value != 0 {
		msgs = append(msgs, "report.leeway is deprecated")
	}

	if conf.Reddit.UnsuspensionInterval.Value != 0 {
		msgs = append(msgs, "reddit.unsuspension_interval is deprecated, use reddit.resurrections_interval instead")
	}
	return msgs
}

// Components returns the corresponding enabled components.
func (conf Configuration) Components() ComponentsConf {
	c := ComponentsConf{}

	redditRequired := map[string]string{
		"id":         conf.Reddit.ID,
		"password":   conf.Reddit.Password,
		"secret":     conf.Reddit.Secret,
		"user agent": conf.Reddit.UserAgent,
		"username":   conf.Reddit.Username,
	}
	var redditInvalid []string
	for name, value := range redditRequired {
		if value == "" {
			redditInvalid = append(redditInvalid, name)
		}
	}

	c.Reddit.Name = "reddit"
	if len(redditInvalid) > 0 {
		c.Reddit.Error = errors.New("missing required fields: " + strings.Join(redditInvalid, ", "))
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

// ComponentConf describes the state of the configuration of a single component.
type ComponentConf struct {
	Enabled bool
	Name    string
	Error   error
}

// String implements Stringer and describes whether the component is enabled or if not what is the error.
func (c ComponentConf) String() string {
	if c.Enabled {
		return fmt.Sprintf("%s: enabled", c.Name)
	}
	return fmt.Sprintf("%s: %s", c.Name, c.Error)
}

// ComponentsConf describes the configuration states of every component of the application.
type ComponentsConf struct {
	Discord ComponentConf
	Reddit  ComponentConf // subsumes several components related to reddit
	Web     ComponentConf
}

// String implements Stringer and describes which components are enabled or if not what is the error.
func (c ComponentsConf) String() string {
	return fmt.Sprintf("%s; %s; %s", c.Discord, c.Reddit, c.Web)
}

// Duration is needed to decode JSON strings to time.Duration.
type Duration struct {
	Value time.Duration
}

// UnmarshalJSON is called by the JSON decoder.
func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return err
	}
	d.Value, err = time.ParseDuration(value)
	return err
}

// Timezone is needed to decode JSON strings to *time.Location.
type Timezone struct {
	Value *time.Location
}

// UnmarshalJSON is called by the JSON decoder.
func (tz *Timezone) UnmarshalJSON(raw []byte) error {
	var value string
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return err
	}
	tz.Value, err = time.LoadLocation(value)
	return err
}
