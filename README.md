# Down Arrows Bot

## User documentation

This bot scans some reddit users, interacts with a discord server of your choice, and build reports about their most downvoted comments.
It is supposed to run continuously, typically on a server, and was designed to use as few resources as possible.
If compiled with the default settings it will only depend on a libc.
It has only been compiled and used on GNU/Linux so far.

To run it simply call the binary. It needs a valid configuration file and to be able to open a database (defaults to `dab.db` in the current directory).
An example [systemd](https://en.wikipedia.org/wiki/Systemd) unit file is provided.

### Compiling

You need at least Go 1.11, which you can download at <https://golang.org/dl/>, and a C compiler (probably gcc).
See <https://golang.org/cmd/go/#hdr-Environment_variables> if you have specific needs.
Once installed, go into the source folder, run `go get -d` to download the dependencies, and then `go build`.

### Web interface

Reports are accessible at `/reports/<year>/<week>`, `/reports/current` and `/reports/lastweek`.
An uncompressed backup of the database can be downloaded at `/backup`;
it serves a cached backup if it is not too old, and otherwise creates one before sending it.

### Discord commands

Commands must start with the configured prefix, and if they take arguments, must be separated from them by a single white space.
Some are reserved to the privileged user.
It accepts usernames as `AGreatUsername` and `/u/AGreatUsername` and `u/AGreatUsername`.

 - `karma` give negative/positive/total karma for the given username
 - `version` post the bot's version
 - `register` try to register a list of usernames; if it starts with the hiding prefix the user will be hidden from reports
 - `unregister` (privileged) unregister one or several user
 - `purge` (privileged) completely remove from the database one or several users
 - `info` information about an user: creation date, registration date, suspension or deletion status, inactive status
 - `hide` hide an user from reports
 - `unhide` don't hide an user from reports
 - `sip` or `sipthebep` quote from sipthebep
 - `sep` or `separator` or `=` post a separation rule

### Command line interface 

Most of the configuration happens in the configuration file.
The command line interface only affects the overall behavior of the program:

 - `-config` Path to the configuration file. Defaults to `./dab.conf.json`
 - `-help` Print the help for command line interface.
 - `-initdb` Initialize the database and exit.
 - `-report` Print the report for last week on the standard output and exit.
 - `-useradd` Add one or multiple usernames to be tracked and exit.

### Configuration file

The configuration file is a JSON file whose top-level data container is a dictionary.
The lists below show every option, where each title is the key for a sub-dictionary,
and follows the pattern `<option's key> <type> (<default>): <explanation>`.
All keys *must* be in lower case.

There are two application-specific types: timezone and duration.
They are JSON strings validated and interpreted according to
<http://golang.localhost/pkg/time/#ParseDuration> and <http://golang.localhost/pkg/time/#LoadLocation>.
For Go templates' syntax, see <http://golang.localhost/pkg/text/template/>.

#### Top level

(**put those options directly inside the main dictionary**)

  - **hide\_prefix** `string` (hide/): prefix you can add to usernames to hide them from reports (used by `-useradd` and on Discord)
  - **timezone** `timezone` (UTC): timezone used to format dates and compute weeks and years

#### Database

 - **backup\_max\_age** `duration` (24h): if the backup is older than that when a backup is requested, the backup will be refreshed
 - **backup\_path** `string` (./dab.db.backup): path to the backup of the database
 - **cleanup\_interval** `duration` (*none*): interval between clean-ups of the database (reduces its size); leave out to disable
 - **path** `string` (./dab.db): path to the database file

#### Discord

 - **admin** `string` (*none*): Discord ID of the privileged user (use Discord's developer mode to get them) (required to enable the Discord component)
 - **general** `string` (*none*): Discord ID of the main channel where loggable links are taken from and welcome messages are posted (required to enable the Discord component)
 - **hide\_prefix** `string` (*none*): Discord-specific hide prefix when registering users (overrides the global hide prefix)
 - **highscores** `string` (*none*): Discord ID of the channel where links to high-scoring comments are posted
 - **highscore\_threshold** `int` (-1000): score at and below which a comment will be linked to in the highscore channel
 - **log** `string` (*none*): Discord ID of the channel where links to comments on reddit are reposted (required to enable the Discord component)
 - **prefix** `string` (!): prefix for commands
 - **token** `string` (*none*): token to connect to Discord; leave out to disable the Discord component 
 - **welcome** `sting` (*none*): Go template of the welcome message; it is provided with two top-level keys,
   `ChannelsID` and `Member`. `ChannelsID` provides `General`, `Log` and `HighScores`, which contains the numeric ID of those channels.
	`Member` provides `ID`, `Name`, and `FQN` (name followed by a discriminator).

#### Reddit

 - **compendium\_update\_interval** `duration` (*none*): interval between each scan of the compendium; leave out to disable
 - **dvt\_interval** `string` (*none*): interval between each check of the downvote sub's new reports; leave out to disable
 - **full\_scan\_interval** `duration` (6h): interval between each scan of all users, inactive or not
 - **id** `string` (*none*): Reddit application ID for the bot (required for users' scanning)
 - **inactivity\_threshold** `duration` (2200h): if a user hasn't commented since that long ago, consider them "inactive" and scan them less often
 - **max\_age** `duration` (24h): don't get more batches of an user's comments if the oldest comment found is older than that
 - **max\_batches** `int` (5): maximum number of batches of comments to get from Reddit for a single user before moving to the next one
 - **password** `string` (*none*): Reddit password for the bot's account (required for users' scanning)
 - **secret** `string` (*none*): Reddit application secret for the bot (required for users' scanning)
 - **unsuspension\_interval** `duration` (15m): interval between each batch of checks for suspended or deleted users; put at `0s` to disable
 - **user\_agent** `string` (*none*): Go template for the user agent of the bot on reddit; `OS` and `Version` are provided (required for users' scanning)
 - **username** `string` (*none*): Reddit username for the bot's account (required for users' scanning)

#### Report

 - **cutoff** `int` (-50): ignore comments whose score is higher than this
 - **leeway** `duration` (12h): shift back the time window for comments' inclusion in the report to include those that were made late
 - **nb\_top** `int` (5): maximum number of users to include in the list of statistics for the report

#### Web

 - **listen** `string` (*none*): `hostname:port` or `ip:port` or `:port` (all interface) specification for the webserver to listen to; leave out to disable

### Example

Note how the last value of a dictionary must not be followed by a comma:

	{

		"timezone": "Europe/London",

		"database": {
			"path": "/var/lib/dab/db.sqlite3",
			"backup_path": "/var/lib/dab/db.sqlite3",
			"cleanup_interval": "6h"
		},

		"reddit": {
			"username": "AGreatUsername",
			"password": "hunter2",
			"id": "XaiUdR5UBKl_FY",
			"secret": "D8PvhefS9ZTZFOUxK-9Bu7iaRLt",
			"user_agent": "{{.OS}}:agreatbot:v1.0 (by /u/AGreatUsername)",
			"dvt_interval": "5m",
			"compendium_update_interval": "1h"
		},

		"discord": {
			"token": "NJx4MJt5ODt5MTk0MzM2Mjc6.DrQECx.pMN84B0UfL2WxxssdxHxx0MxxK8",
			"admin": "308148615101724003",
			"general": "508169151894056221",
			"log": "508869940013213344",
			"highscores": "508263683211452578",
			"welcome": "Hello <@{{.Member.ID}}>!"
		},

		"web": {
			"listen": "localhost:1234"
		}

	}

## Developer documentation

Go was designed to be easy to learn, especially if you already have experience with an imperative language.
You should be able to easily find on the web many resources for your knowledge level.
If you already have some programming experience and want quickly learn the basics try <https://tour.golang.org/>.
If you are a beginner and really don't know where to start you may try <https://www.golang-book.com/books/intro>.

This readme is written in markdown and is compatible with github-flavored markdown and pandoc-flavored markdown.

### Conventions

 - always use go fmt (you may be able to configure or install a plugin for your editor or IDE to do that automatically)
 - export as few struct fields as possible and type them in camel case
 - sort by name struct fields, or if they are really about different things, separate them with a blank line
 - use snake case for variables inside functions
 - use camel case for constants, types and variables at package-level
 - start names at package-level with a capitalized letter only if they are used in another file
 - unless you add a lot of code, keep everything inside the main package (having to deal with multiple packages complicates things)
 - lay out files in that order: constants and variables, init function if any, types, file-specific types, auxilliary types + new function + methods, main type + new function + methods. Sometimes it is unclear what type is the most important type in a file (eg. their source is roughly the same length); lay things out however it seems logical

### Architecture

This application has several components that run independently and communicate either through the database layer or through channels.
They are managed by the `DAB` data structure; it checks what the user wants to do, what components can be launched according to the configuration file,
propagates the root context for proper cancellation, and waits for each component to shut down and returns errors.
It also logs what components are enabled and why some are disabled.
Its main tool to achieve that is the `TaskGroup` data structure, which allows to manage groups of functions ran as goroutines and which have the following type: `func (context.Context) error`.
Contexts propagate cancellation signals, and task groups allow to easily propagate a context and wait for each cancelled goroutine.

A component is a data structre which has one method with the `func (context.Context) error` signature (in which case it is called `Run`), or several.
See the `DAB` data structure definition for the list of components.
Since goroutines that write to channels would block if the goroutines reading the channels were to be stopped before them,
a task group only for writers is used and stopped before the task group for readers, allowing for proper and orderly shutdown.
Make sure that any function spawned by a task group properly returns if it receives a cancellation signal at any point
(it may or may not return the cancellation error, it doesn't matter).

`Storage` is not a component, it is a layer, and is passed by reference to other data structures.
Since its methods panic on most errors (because most errors would mean a serious issue with the database, requiring to immediately stop),
it must be closed last, after all components have been stopped.
Its instantiation shouldn't panic though, it needs to properly report errors and close the database.

### TODO

 - auto post reports to a specific sub and maintain its wiki
 - better browsability of the web reports
 - replace blackfriday with snudown
