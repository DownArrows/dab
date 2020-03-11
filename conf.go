package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Defaults defines the default configuration of the whole application.
const Defaults string = `{
	"log_level": "Info",
	"hide_prefix": "hide/",
	"timezone": "UTC",

	"database": {
		"backup": {
			"main": "./dab.db.backup",
			"max_age": "24h",
			"secrets": "./dab.secrets.backup"
		},
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
		"dirty_reads": true,
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
		"db_optimize": "2h",
		"default_limit": 100,
		"dirty_reads": true,
		"max_limit": 1000,
		"nb_db_conn": 10,
		"tls": {
			"helper": {
				"listen": ":80"
			}
		}
	}
}`

// ListenFDsEnvVar is the name of the environment variable that indicates file descriptors to listen on.
const ListenFDsEnvVar = "LISTEN_FDS"

// StorageConf describes the configuration of the Storage layer.
type StorageConf struct {
	Backup struct {
		Main    string   `json:"main"`
		MaxAge  Duration `json:"max_age"`
		Secrets string   `json:"secrets"`
	} `json:"backup"`
	BackupMaxAge    Duration  `json:"backup_max_age"`
	BackupPath      string    `json:"backup_path"`
	CleanupInterval Duration  `json:"cleanup_interval"`
	LogLevel        string    `json:"log_level"`
	Path            string    `json:"path"`
	Retry           RetryConf `json:"retry_connection"`
	SecretsPath     string    `json:"secrets_path"`
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
	DirtyReads     bool     `json:"dirty_reads"`
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
	DBOptimize   Duration `json:"db_optimize"`
	DefaultLimit uint     `json:"default_limit"`
	DirtyReads   bool     `json:"dirty_reads"`
	IPHeader     string   `json:"ip_header"`
	Listen       string   `json:"listen"`
	ListenFDs    uint     `json:"-"`
	MaxLimit     uint     `json:"max_limit"`
	NbDBConn     uint     `json:"nb_db_conn"`
	RootDir      string   `json:"root_dir"`
	TLS          TLSConf  `json:"tls"`
}

// ListenFDs checks for an environment variable that allows to
// pass file descriptors for the web server to listen to (used by systemd socket activation)
func (wc WebConf) getListenFDs() (uint, error) {
	rawNum := os.Getenv(ListenFDsEnvVar)
	if rawNum == "" {
		return 0, nil
	}
	num, err := strconv.Atoi(rawNum)
	if err != nil {
		return 0, fmt.Errorf("failed to read a valid number in the environment variable %q: %v", ListenFDsEnvVar, err)
	} else if num < 0 {
		return 0, fmt.Errorf("the environment variable %q must not be less than 0 (got %d)", ListenFDsEnvVar, num)
	} else if wc.TLS.Enabled() && wc.TLS.Helper.Enabled() {
		if num != 2 {
			cond := "if the web server has both TLS and the redirector enabled"
			requirement := "socket activation through file descriptors must provide two and only two sockets"
			return 0, fmt.Errorf(cond + ", " + requirement)
		}
	} else if num > 1 {
		return 0, fmt.Errorf("the web server can only listen on a single socket")
	} // now the number is necessarily 0, 1, or 2

	return uint(num), nil
}

// TLSConf describes the configuration of TLS for the webserver,
// and can check whether it's active or not.
type TLSConf struct {
	ACME []string `json:"acme"`
	Cert string   `json:"cert"`
	Key  string   `json:"key"`

	Helper TLSHelperConf `json:"helper"`
}

// Enabled checks whether TLS is enabled.
func (tc TLSConf) Enabled() bool {
	return tc.ACMEEnabled() || tc.CertsEnabled()
}

// ACMEEnabled checks whether TLS is enabled with ACME.
func (tc TLSConf) ACMEEnabled() bool {
	return tc.ACME != nil && len(tc.ACME) > 0
}

// CertsEnabled checks whether the certificate files for TLS are set.
func (tc TLSConf) CertsEnabled() bool {
	return tc.Cert != "" && tc.Key != ""
}

// CertsPartiallyEnabled checks whether the TLS certificate configuration is only
// partially filled-in, which could indicate a mistake in the configuration file.
func (tc TLSConf) CertsPartiallyEnabled() bool {
	return !tc.Enabled() && (tc.Cert != "" || tc.Key != "")
}

// TLSHelperConf describes the configuration of the TLS helper.
type TLSHelperConf struct {
	Listen    string `json:"listen"`
	ListenFDs uint   `json:"-"`
	Target    string `json:"target"`
	IPHeader  string `json:"-"`
}

// Enabled checks whether the TLS helper is supposed to be active.
func (rc TLSHelperConf) Enabled() bool {
	return rc.Listen != "" && rc.Target != ""
}

// Configuration holds the configuration for the whole application.
type Configuration struct {
	HidePrefix string   `json:"hide_prefix"`
	LogLevel   string   `json:"log_level"`
	Timezone   Timezone `json:"timezone"`

	Database struct {
		LogLevel string `json:"log_level"`
		StorageConf
	}

	Reddit struct {
		RedditAuth
		RedditScannerConf
		RedditUsersConf
		DVTInterval      Duration           `json:"dvt_interval"` // Deprecated
		LogLevel         string             `json:"log_level"`
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
		LogLevel           string    `json:"log_level"`
		Retry              RetryConf `json:"retry_connection"`
	}

	Web struct {
		LogLevel string `json:"log_level"`
		WebConf
	}
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

	// Compatibility with a deprecated option
	if conf.Database.Backup.MaxAge.IsZero() {
		conf.Database.Backup.MaxAge = conf.Database.BackupMaxAge
	}
	// Compatibility with a deprecated option
	if conf.Database.Backup.Main == "" {
		conf.Database.Backup.Main = conf.Database.BackupPath
	}
	if conf.Database.SecretsPath == "" {
		base := conf.Database.Path
		ext := filepath.Ext(base)
		conf.Database.SecretsPath = filepath.Join(filepath.Dir(base), "secrets") + ext
	}

	conf.Web.ListenFDs, err = conf.Web.getListenFDs()
	if err != nil {
		return conf, err
	}
	conf.Web.TLS.Helper.ListenFDs = conf.Web.ListenFDs
	conf.Web.TLS.Helper.IPHeader = conf.Web.IPHeader

	conf.Compendium.NbTop = conf.Report.NbTop

	conf.Reddit.RedditScannerConf.HighScoreThreshold = conf.Discord.HighScoreThreshold
	if conf.Discord.DiscordBotConf.HidePrefix == "" {
		conf.Discord.DiscordBotConf.HidePrefix = conf.HidePrefix
	}

	if conf.Discord.DiscordBotConf.Graveyard == "" {
		conf.Discord.DiscordBotConf.Graveyard = conf.Discord.DiscordBotConf.General
	}

	if !conf.Reddit.UnsuspensionInterval.IsZero() {
		conf.Reddit.ResurrectionsInterval.Value = conf.Reddit.UnsuspensionInterval.Value
	}

	if conf.Database.LogLevel == "" {
		conf.Database.LogLevel = conf.LogLevel
	}
	if conf.Discord.LogLevel == "" {
		conf.Discord.LogLevel = conf.LogLevel
	}
	if conf.Reddit.LogLevel == "" {
		conf.Reddit.LogLevel = conf.LogLevel
	}
	if conf.Web.LogLevel == "" {
		conf.Web.LogLevel = conf.LogLevel
	}

	return conf, nil
}

// HasSaneValues protect against values that are very likely to be mistakes.
func (conf Configuration) HasSaneValues() *ErrorGroup {
	errs := NewErrorGroup()

	if conf.HidePrefix == "" {
		errs.Add(errors.New("prefix for hidden users can't be an empty string"))
	}
	if conf.Database.Backup.MaxAge.Value < time.Hour {
		errs.Add(errors.New("backup max age before renewal can't be less than an hour"))
	}
	if conf.Database.Path == conf.Database.BackupPath {
		errs.Add(errors.New("backup path can't be the same as the database's path"))
	}
	if conf.Database.Backup.Secrets == "" {
		errs.Add(errors.New("the backup path for secrets needs to be set"))
	}
	if val := conf.Database.CleanupInterval.Value; val != 0 && val < time.Minute {
		errs.Add(errors.New("interval between database cleanups can't be less than a minute"))
	}
	if conf.Reddit.FullScanInterval.Value < time.Hour {
		errs.Add(errors.New("interval for the full scan can't be less an hour"))
	}
	if conf.Reddit.InactivityThreshold.Value < 24*time.Hour {
		errs.Add(errors.New("inactivity threshold can't be less than a day"))
	}
	if conf.Reddit.MaxAge.Value < 24*time.Hour {
		errs.Add(errors.New("max comment age for further scanning can't be less than a day"))
	}
	if conf.Reddit.HighScoreThreshold > -1 {
		errs.Add(errors.New("high-score threshold can't be positive"))
	}
	if val := conf.Reddit.ResurrectionsInterval.Value; val != 0 && val < time.Minute {
		errs.Add(errors.New("interval between batches of checks of resurrections of users can't be less than a minute if non-zero"))
	}
	if conf.Report.Leeway.Value < 0 { // Deprecated
		errs.Add(errors.New("reports' leeway can't be negative"))
	}
	if conf.Report.CutOff > 0 {
		errs.Add(errors.New("reports' cut-off can't be higher than 0"))
	}
	if conf.Web.DBOptimize.Value < 5*time.Minute {
		errs.Add(errors.New("the duration of the optimization of the web server's connections to the database can't be less than 5 minutes"))
	}
	if conf.Web.NbDBConn == 0 {
		errs.Add(errors.New("the number of database connections from the web server can't be 0"))
	}

	return errs
}

// Warnings gets warnings about the configuration.
func (conf Configuration) Warnings() []string {
	var msgs []string

	if conf.Web.TLS.ACME == nil && conf.Web.TLS.CertsPartiallyEnabled() {
		msgs = append(msgs, "web.tls file certificate is only partially configured (missing certificate or key), TLS will not be enabled")
	}

	return msgs
}

// Deprecations returns a slice of deprecation messages.
func (conf Configuration) Deprecations() []string {
	var msgs []string

	if conf.Discord.Admin != "" {
		msgs = append(msgs, "discord.admin is deprecated, use discord.privileged_role instead")
	}

	if !conf.Reddit.DVTInterval.IsZero() {
		msgs = append(msgs, "reddit.dvt_interval is deprecated")
	}

	if conf.Reddit.WatchSubmissions != nil {
		msgs = append(msgs, "reddit.watch_submissions is deprecated")
	}

	if !conf.Reddit.CompendiumUpdateInterval.IsZero() {
		msgs = append(msgs, "reddit.compendium_update_interval is deprecated")
	}

	emptyCompendiumConf := WatchCompendiumConf{}
	if conf.Reddit.Compendium != emptyCompendiumConf {
		msgs = append(msgs, "reddit.compendium is deprecated")
	}

	if !conf.Report.Leeway.IsZero() {
		msgs = append(msgs, "report.leeway is deprecated")
	}

	if !conf.Reddit.UnsuspensionInterval.IsZero() {
		msgs = append(msgs, "reddit.unsuspension_interval is deprecated, use reddit.resurrections_interval instead")
	}

	if !conf.Database.BackupMaxAge.IsZero() {
		msgs = append(msgs, "database.backup_max_age is deprecated, use database.backup.max_age instead")
	}

	if conf.Database.BackupPath != "" {
		msgs = append(msgs, "database.backup_path is deprecated, use database.backup.main instead")
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
	if conf.Web.Listen == "" && conf.Web.ListenFDs == 0 {
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

// IsZero tests whether the Duration value is zero.
func (d *Duration) IsZero() bool {
	return d.Value == 0
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
