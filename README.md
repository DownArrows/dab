This bot scans some reddit users, interacts with a discord server of your choice, and build reports about their most downvoted comments.
It is supposed to run continuously, typically on a server, and was designed to use as few resources as possible.

Table of contents:

 - [End user manual](#end-user-manual)
    - [Web interface](#web-interface)
    - [Discord commands](#discord-commands)
    - [Database](#database)
 - [Administrator manual](#administrator-manual)
    - [Compiling](#compiling)
    - [Reddit and Discord credentials](#reddit-and-discord-credentials)
    - [Serving custom files](#serving-custom-files)
    - [Running](#running)
    - [Maintenance](#maintenance)
    - [Command line interface](#command-line-interface)
    - [Configuration](#configuration)
    - [Sample configuration](#sample-configuration)
    - [Database identification](#database-management)
 - [Developer manual](#developer-manual)
    - [Conventions](#conventions)
    - [Architecture](#architecture)
    - [Using the database](#using-the-database)
    - [Database schema](#database-schema)
    - [TODO](#todo)

# End user manual

## Web interface

 - `/` shows a custom web page, file, or directory listing, if the administrator has enabled this feature
 - `/reports/<year>/<week number>` shows the report for the specified week of the year,
   where the week number is an [ISO week number](https://en.wikipedia.org/wiki/ISO_week_date)
 - `/reports/current` redirects to the report of the current week
 - `/reports/lastweek` redirects to the report of the previous week
 - `/reports/stats/<year>/<week number>` shows all statistics for the specified week
 - `/compendium` summarizes data about all users
 - `/compendium/<user name>` shows data for a single user
 - `/compendium/comments` shows all comments sorted by score in reverse order
 - `/compendium/<user name>/comments` shows all comments of a single user sorted by score in reverse order

## Discord commands

Commands must start with the configured prefix (defaults to `!`), and if they take arguments, must be separated from them by a single white space.
Some are reserved to privileged users (the server's owner and a specific role).
If they accept Reddit user names, they accept them as `AGreatUsername`, `/u/AGreatUsername`, and `u/AGreatUsername`.

 - `ban` (privileged) ban the mentioned user with an optional reason
 - `delete` (privileged) mass-delete messages in the current channel;
    the first argument is the number to delete, and the optional second one is the offset at which to start the deletion
 - `hide` hide a user from reports
 - `info` information about a user: creation date, registration date, suspension or deletion status,
    inactive status, and which discord user registered the user (only if known or any did)
 - `invite` (privileged) create an invite limited to a single use within a week
 - `karma` give negative/positive/total karma for the given user name
 - `purge` (privileged) completely remove from the database one or several users
 - `register` try to register a list of user names; if it starts with the hiding prefix the user will be hidden from reports
 - `reregister` (privileged) re-register one or several user that were previously unregistered
 - `sep` or `separator` or `=` post a separation rule
 - `sip` or `sipthebep` quote from sipthebep
 - `time` current time in the bot's configured time zone
 - `unhide` don't hide a user from reports
 - `unregister` (privileged) unregister one or several user
 - `version` post the bot's version

If you post a link in the main channel that contains a link to a comment on reddit,
it will be reposted on the log channel if the administrator of the bot has enabled this feature.
To prevent that, add the command `nolog` anywhere in your comment.

## Database

The database contains no private data and thus a backup can be publicly shared.
If you have such a backup and want to browse the database, you can use something like the [DB Browser for SQLite](https://sqlitebrowser.org/).
See [the administrator section](#database-identification) and [the developer section](#database-schema) for detailed documentation about it.

# Administrator manual

DAB has only been compiled and used on GNU/Linux so far.
It should theoretically work on other platforms.

The configuration is organized around three main components — discord, reddit, the web server — that can be activated independently.
You can only launch the web server on an already populated database, or only scan reddit and don't connect to discord.
See the section on the available configuration options to see what is necessary to enable each component.

Versioning follows [semver](https://semver.org/) and applies to everything documented in the end user and administrator manuals.

## Compiling

You need at least Go 1.11, which you can download at <https://golang.org/dl/>, as well as git, the gcc compiler, and the headers of a C library.
On Debian-based distributions you can get all those dependencies with `apt install golang gcc libc-dev git`.
The version of Go may not be high enough, in which case download it from the official website and install it manually.
See <https://golang.org/cmd/go/#hdr-Environment_variables> if you have specific needs.
Once installed, go into the source folder, run `go get -d` to download the dependencies, and then `go build` (that may take a few minutes the first time).

## Reddit and Discord credentials

For the Discord component, you first need an account,
then go on https://discordapp.com/developers/, create an application, add a bot to it, and get its client ID and token.
Then put the token into DAB's configuration file, and substitute `CLIENT_ID` for the actual one in the following URL:
`https://discordapp.com/oauth2/authorize?client_id=CLIENT_ID&scope=bot&permissions=8`.
Open this URL in your browser and you should be prompted with the discord servers where you can invite bots.
It may be possible that by the time you read this the exact steps have changed;
if it did, refer to Discord's help and try to search around on the web.
To get the numeric IDs of the channels you want to enable, go in your account's settings,
enable the developer mode, and right click on each channel to get their ID (they must all be in the same server).

For the Reddit component, you also need an account, but this account will be used directly by the bot;
if you plan to do more than test it, it may be better to create an account just for it.
Once you have an account, you need to create a new application of the type "personal script".
On the old design, go in the account's preferences, tab "apps" and click on "are you a developer? create an app...",
and select the "script" option. If it whines about not having an URL, give it `http://example.org/`, it won't matter.
On the redesign, also go in your account's settings, and in the "Privacy & Security" tab go on "App authorization".
At the time of writing, this will redirect you to the old design.
Once you got a client ID and a secret, put the account's user name, its password, the client ID and the secret inside the configuration file.

## Serving custom files

The web server can serve any file under a directory configured by `web.root_dir`.
The files can have any name that doesn't clash with the application's URLs.
By default it generates an index of the root directory and any of its sub-directory,
or serves `index.html` if it is present at their root.
Therefore do not put anything sensitive in the root directory.
If you want to use the application's style sheets, you can link to them without adding a version string.
See the sample makefile for an example of the generation of a file that can be put in the application's
root directory and take advantage of the application's style sheets.

## Running

To run the bot simply call the binary. It will expect a file named `dab.conf.json` in the current directory.
To use another path for the configuration file use `dab -config /your/custom/path/dab.conf.json` (note that the file can have any name).
It needs a valid configuration file and to be able to open the database it is configured with (defaults to `dab.db` in the current directory).

If you want to run it with [systemd](https://en.wikipedia.org/wiki/Systemd), here is a sample unit file to get you started:

	[Unit]
	Description=DAB (Down Arrows Bot)
	After=network.target

	[Install]
	WantedBy=multi-user.target

	[Service]
	ExecStart=/usr/local/bin/dab -config /etc/dab.conf.json
	Restart=on-failure

It also partially supports systemd's socket activation, with a limitation to a single socket for the moment being.

The bot shuts down on the following UNIX signals: SIGINT, SIGTERM, and SIGKILL.
On Windows it will not respond to Ctrl+C.

## Web

## Maintenance

If the bot has been offline for a while, it will pick everything back up where it left,
save for messages on Discord and comments of Reddit users that got banned or deleted in the meantime.

To backup the database, **do not** copy the file it opens (given in the `database` section of the config file, option `path`).
Only use the built-in backup system by downloading the file with HTTP (which must be enabled in the `web` section by setting `listen`).
It serves a cached backup if it is not too old (`backup_max_age` in the configuration file), and otherwise creates one before sending it.
This can be used in a backup script called by cron, like so:

	#!/bin/sh
	set -e
	bak="/srv/dab/dab.db.bak"
	wget -O"$bak" -q http://localhost:3499/backup
	rsync -e "ssh -i /root/.ssh/backup" "$bak" backup@anothercomputer:/var/backups/dab.sqlite3

If after the bot has stopped there are files ending in `-shm`, `-wal` and `-journal` in the folder containing the database file,
**do not** delete them, they probably contain data and deleting them could leave the database corrupted.
Instead leave them with the database, then run the bot normally,
or if you just want to repair the database, run it with the `-initdb` option.
Those files are also present when the bot is running, which is perfectly normal.
For more information about them see <https://sqlite.org/tempfiles.html>.

## Command line interface

Most of the configuration happens in the configuration file.
The command line interface only affects the overall behavior of the program:

 - `-config` Path to the configuration file. Defaults to `./dab.conf.json`
 - `-help` Print the help for the command line interface.
 - `-initdb` Initialize the database and exit.
 - `-log` (deprecated) Logging level (`Error`, `Info`, `Debug`). Defaults to `Info`.
 - `-report` Print the report for last week on the standard output and exit.
 - `-useradd` (deprecated) Add one or multiple user names separated by a white space or a comma to be tracked and exit.

## Configuration

The configuration file is a JSON file whose top-level data container is a dictionary.
The list below shows every option, where a sub-list corresponds to a dictionary,
and each option follows the pattern `<option's key> <type> (<default>): <explanation>`.
All keys *must* be in lower case.

There are three application-specific types, which are JSON strings interpreted according to Go functions:
[timezone](http://golang.org/pkg/time/#LoadLocation),
[duration](http://golang.org/pkg/time/#ParseDuration),
and [template](http://golang.org/pkg/text/template/).

 - `hide_prefix` *string* (hide/): prefix you can add to user names to hide them from reports (used by `-useradd` and on Discord)
 - `log_level` *string* (Info): top-level logging level and default logging level for components
   ("Fatal", "Error", "Info", "Debug", case-insensitive)
 - `timezone` *timezone* (UTC): timezone used to format dates and compute weeks and years
 - `database`
    - `backup_max_age` *duration* (24h): if the backup is older than that when a backup is requested,
      the backup will be refreshed; must be at least one hour
    - `backup_path` *string* (./dab.db.backup): path to the backup of the database
    - `cleanup_interval` *duration* (30m): interval between clean-ups of the database (reduces its size and optimizes queries);
       put at `0s` to disable, else must be at least one minute
    - `log_level` *string* (*parent `log_level`*): logging level for this component ("Fatal", "Error", "Info", "Debug", case-insensitive)
    - `path` *string* (./dab.db): path to the database file
    - `retry_connection` *dictionary*:
       - `times` *int* (25): maximum number of times to try to create a connection to the database; use -1 for infinite retries
       - `max_interval` *duration* (10s): maximum wait between connection retries
       - `reset_after` *duration* (*none*): time after which the restart count and the backoff are reset
    - `timeout` *duration* (15s): timeout on the [database' lock](https://sqlite.org/c3ref/busy_timeout.html)
 - `discord`
    - `admin` *string* (*none*): Discord ID of the privileged user (use Discord's developer mode to get them);
      if empty will use the owner of the channels' server, and if no channel is enabled, will disable privileged commands.
      **Deprecated**: starting with version 1.8.0 this option has no effect.
    - `dirty_reads` *bool* (true): allow reading inconsistent data from the database in exchange of better concurrency
    - `general` *string* (*none*): Discord ID of the main channel where loggable links are taken from and welcome messages are posted;
      required to have welcome messages and logged links, disabled if left empty
    - `graveyard` *string* (copy of `general`): Discord ID of the channel where to post messages about (un)suspensions and (un)deletions
    - `hide_prefix` *string* (*none*): Discord-specific hide prefix when registering users (overrides the global hide prefix)
    - `highscores` *string* (*none*): Discord ID of the channel where links to high-scoring comments are posted; disabled if left empty
    - `highscore_threshold` *int* (-1000): score at and below which a comment will be linked to in the highscore channel
    - `log` *string* (*none*): Discord ID of the channel where links to comments on reddit are reposted; disabled if left empty
    - `log_level` *string* (*parent `log_level`*): logging level for this component ("Fatal", "Error", "Info", "Debug", case-insensitive)
    - `prefix` *string* (!): prefix for commands
    - `privileged_role` *string* (*none*): Discord ID of the role that can use privileged commands, along with the server's owner
    - `retry_connection` *dictionary*:
       - `times` *int* (5): maximum number of times to try to connect to Discord; use -1 for infinite retries
       - `max_interval` *duration* (2m): maximum wait between connection retries
       - `reset_after` *duration* (2h): time after which the restart count and the backoff are reset
    - `token` *string* (*none*): token to connect to Discord; leave out to disable the Discord component
    - `welcome` *template* (*none*): template of the welcome message; it is provided with three top-level keys,
      `ChannelsID`, `Member`, and `BotID`. `ChannelsID` provides `General`, `Log` and `HighScores`, which contains the numeric ID of those channels.
      `Member` provides `ID`, `Name`, and `FQN` (name followed by a discriminator).
      `BotID` is the ID of the bot so that you can mention it with `<@{{.BotID}}>`.
      Welcome messages are disabled if the template is empty or not set
 - `reddit`
    - `compendium` *dictionary* **Deprecated**:
       - `sub` *string* (*none*): sub on which the compendium can be found; leave out to disable scans of the compendium
       - `update_interval` *duration* (*none*): interval between each scan of the compendium;
         leave out to disable, else must be at least an hour
       - `reset_after` *duration* (2h): time after which the restart count and the backoff are reset
    - `dvt_interval` *string* (*none*): interval between each check of the downvote sub's new reports;
      leave out to disable, else must be at least a minute.
      **Deprecated**: starting with version 1.10.0 this option has no effect.
    - `full_scan_interval` *duration* (6h): interval between each scan of all users, inactive or not
    - `id` *string* (*none*): Reddit application ID for the bot; leave out to disable the Reddit component
    - `inactivity_threshold` *duration* (2200h): if a user hasn't commented since that long ago,
      consider them "inactive" and scan them less often; must be at least one day
    - `log_level` *string* (*parent `log_level`*): logging level for this component ("Fatal", "Error", "Info", "Debug", case-insensitive)
    - `max_age` *duration* (24h): don't get more batches of a user's comments if the oldest comment found is older than that;
      must be at least one day
    - `max_batches` *integer* (5): maximum number of batches of comments to get from Reddit for a single user before moving to the next one
    - `password` *string* (*none*): Reddit password for the bot's account; leave out to disable the Reddit component
    - `resurrections_interval` *duration* (*none*): interval between each batch of checks for suspended or deleted users;
    - `retry_connection` *dictionary*:
       - `times` *int* (10): maximum number of times to try to connect to Reddit; use -1 for infinite retries
       - `max_interval` *duration* (5m): maximum wait between connection retries
    - `secret` *string* (*none*): Reddit application secret for the bot; leave out to disable the Reddit component
    - `unsuspension_interval` *duration* (*none*) **Deprecated**: see `resurrections_interval`
      leave out to disable, else must be at least one minute
    - `user_agent` *template* (*none*): user agent of the bot on reddit; `OS` and `Version` are provided; leave out to disable the Reddit component
    - `username` *string* (*none*): Reddit user name for the bot's account; leave out to disable the Reddit component
    - `watch_submissions` *array of dictionaries* **Deprecated**:
       - `target` *string* (*none*): a user name whose submissions will be watched, or a sub;
          a username must start with `/u/`, and a sub with `/r/`
       - `interval` *duration* (*none*): interval between each scan of the target
 - `report`
    - `cutoff` *integer* (-50): ignore comments whose score is higher than this
    - `leeway` *duration* (0h) **Deprecated**: shift back the time window for comments' inclusion in the report
      to include those that were made late; cannot be negative. Deprecated due to lack of usefulness
    - `nb_top` *integer* (5): maximum number of users to include in the list of statistics for the report
      (also used for the top in the compendium)
 - `web`
    - `default_limit` *integer* (100): default number of items per page of paginated data
    - `dirty_reads` *bool* (true): allow reading inconsistent data from the database in exchange of better concurrency
    - `ip_header` *string* (*none*): HTTP header that contains the true IP, so that logs can be accurate (use if behind a reverse-proxy)
    - `listen` *string* (*none*): `hostname:port` or `ip:port` or `:port` (all interfaces)
      specification for the webserver to listen to; leave out to disable
    - `log_level` *string* (*parent `log_level`*): logging level for this component ("Fatal", "Error", "Info", "Debug", case-insensitive)
    - `max_limit` *integer* (1000): maximum number of items per page of paginated data
    - `nb_db_conn` *integer* (10): number of database connections open for the web server
    - `root_dir` *string* (*none*): root directory that is served at the root URL, with automatic directory index generation,
       and which serves `index.html` as the root of a directory if present

## Sample configuration

Note how the last value of a dictionary must not be followed by a comma:

	{

		"timezone": "America/Chicago",

		"database": {
			"path":        "/var/lib/dab/db.sqlite3",
			"backup_path": "/var/lib/dab/db.sqlite3.backup"
		},

		"reddit": {
			"username":   "AGreatUsername",
			"password":   "hunter2",
			"id":         "XaiUdR5UBKl_FY",
			"secret":     "D8PvhefS9ZTZFOUxK-9Bu7iaRLt",
			"user_agent": "{{.OS}}:agreatbot:v{{.Version}} (by /u/AGreatUsername)",
			"unsuspension_interval": "15m"
		},

		"discord": {
			"token":      "NJx4MJt5ODt5MTk0MzM2Mjc6.DrQECx.pMN84B0UfL2WxxssdxHxx0MxxK8",
			"general":    "508169151894056221",
			"log":        "508869940013213344",
			"highscores": "508263683211452578",
			"privileged_role": "653243081214462080",
			"welcome": "Hello <@{{.Member.ID}}>!"
		},

		"web": {
			"log_level": "info",
			"listen": "localhost:3499"
		}

	}

## Database identification

The database is an SQLite file which contains in its header a specific application
format and the version of DAB that last wrote into it.

You can use a file identification tool (like `file` on UNIX-like OSes) to get information
about the database. It has the application ID `3499` (or `dab` in hexadecimal),
and the version of the bot that last wrote into the database is encoded as an integer.
You can check whether an SQLite file is a valid DAB database and decode
its version with the following python (2 and 3) script:

	import sys
	import struct
	db_path = sys.argv[1]
	with open(db_path, "rb") as f:
		 # https://www.sqlite.org/fileformat2.html#database_header
		 if f.read(16) != b"SQLite format 3\x00":
			  print("'{path}' is not an SQLite 3 file.".format(path=db_path))
			  exit(1)
		 f.seek(68) # application ID
		 app_id = struct.unpack(">L", f.read(4))[0]
		 if app_id != 0xdab:
			  msg = "'{path}' has incorrect application ID 0x{id:x} (expected 0xdab)."
			  print(msg.format(path=db_path, id=app_id))
			  exit(1)
		 f.seek(60) # user version
		 v = bytearray(f.read(4))
		 msg = "File last written by DAB version {major}.{minor}.{bugfix}."
		 print(msg.format(major=v[1], minor=v[2], bugfix=v[3]))

Save this script in a file, for example named `dab_db.py`, and run it on `dab.db` with `python dab_db.py dab.db`.

Starting with 1.7, if it finds in the database a version more recent that itself, it will refuse to start.
If you have a database written by DAB prior to 1.6, or if you manipulated the database and removed the
correct headers, you can set things straight with the following queries:

	PRAGMA application_id = 0xdab;
	-- Sets version to 1.6.0, edit for the version you target;
	PRAGMA application_id = 1 * 65536 + 6 * 256 + 0

# Developer manual

Go was designed to be easy to learn, especially if you already have experience with an imperative language.
You should be able to easily find on the web many resources for your knowledge level.
If you already have some programming experience and want quickly learn the basics try <https://tour.golang.org/>.
If you are a beginner and really don't know where to start you may try <https://www.golang-book.com/books/intro>.

This readme is written in markdown and is compatible with github-flavored markdown and pandoc-flavored markdown.

## Conventions

 - always use go fmt (you may be able to configure or install a plugin for your editor or IDE to do that automatically)
 - type struct fields and methods names in camel case,
   and only use pascal case if they are to be used outside of the struct's methods and creation function(s)
   (we treat unexported fields and methods like private methods and attributes in object-oriented programming,
   although since everything is in the same package at this point it's nothing more than a convention that isn't enforced by the compiler)
 - comment everything that's exported
 - sort by name struct fields, or if they are really about different things, separate them with a blank line
 - use camel case for constants, types and variables at package-level
 - use pascal case for them if they are used in another file
 - unless you add a lot of code, keep everything inside the main package (having to deal with multiple packages complicates things)
 - lay out files in that order, unless there is no clear central type,
 in which case just do whatever makes the most sense when reading from top to bottom:
    1. constants, variables, the init function if any
    2. types used in other files
    3. file-specific types followed by the function to create them and then their methods
    4. the central type, followed by the function to create it, then the methods
 - increment the version according to semver by changing the variable `Version` in `dab.go`

## Architecture

*(NB: The architecture is mostly inspired by Erlang/OTP and the Trio asynchronous framework for python.)*

The main problem is coordinating multiple long-running goroutines with methods acting on shared state,
while keeping coupling low and using injection of dependencies.
The wiring up of the "layers" and "components" is done by `DownArrowsBot` in `dab.go`;
this is the data structure and methods you want to read if you want to understand the architecture.

A "layer" is a data structure with a collection of methods that can be called from any goroutine at any time.
That means they have to be thread-safe, and if at all possible, be passed by value rather by reference.
They don't communicate via channels, they are simply passed around to the layer or component that uses them (ie their client).
For clarity and decoupling, we may define per-client interfaces containing only the methods that are used by each client.
Where components communicate via channels, layers communicate via interfaces.

A "component" is a data structure with one or several methods that are also "tasks" (see next paragraph).
By extension, we call a component (or "main component") several components and layers related to the same topic (eg. communicating with Reddit),
especially in the context of the configuration where they all depend on the same set of settings.

We call a `Task` (see definition in `concurrency.go`) a function that can be managed by a `TaskGroup`.
The goal of a task group is to launch a function as a goroutine with a [context](https://golang.org/pkg/context/)
and wait for it to return normally or with an error (this was inspired by Trio's nurseries).
By extension, we call a task any function that can be turned into a proper task with a trivial wrapping function.
They communicate together through channels that are passed either via a closure or via the data structure they are attached to.

## Using the database

The database is used with a non-standard driver that makes use of features specific to SQLite.
Its interface is wrapped in custom data structures to allow for context-based cancellation and less code repetition.
See the [driver's documentation](https://godoc.org/github.com/bvinc/go-sqlite-lite/sqlite3) and the interface
`SQLiteConn` to get an overview of what each connection can do.
Note that mass insertion isn't automated, since it may require flexibility; you're expected to
wrap everything in a transaction, prepare the statement, defer its closing, and after each execution clear the bindings.
Moreover, instead of pooling connections automatically, you're expected to hold them for each long-running goroutine,
or use a custom pooling mechanism (a basic one is defined by `SQLiteConnPool`).

The application also defines a small framework in `SQLiteDatabase`
to create and manage SQLite databases, and enable their useful non-default features.
It checks and writes the application's identifier, its version, runs a basic migration system,
and checks the data's integrity on every startup.
Files that start with `sqlite` define the code that isn't really specific to the application.
If you need to add new methods to do queries on the database, you probably only need to modify `storage_conn.go`.

## Database schema

 - `user_archive`: table of all registered reddit users, deleted or not
    - `name`: name of the user
    - `created`: UNIX timestamp of the creation date according to reddit
    - `not_found`: TRUE if trying to get information about this user resulted in a 404 not found error
    - `suspended`: TRUE if suspended according to reddit's API
    - `added`: UNIX timestamp of the date when the user was added to the database
    - `batch_size`: Number of comments below the max age on the last scan
    - `deleted`: TRUE if user is marked as deleted (will not be scanned or included in reports anymore)
    - `hidden`: TRUE if user is scanned but not shown in reports
    - `inactive`: TRUE if considered inactive
    - `last_scan`: UNIX timestamp of the last time this user was scanned
    - `new`: TRUE until all reachable pages of comments of that user have been saved
    - `position`: reddit-specific ID of the position in the pages of comments of that user
 - `users`: view of the `user_archive` table without deleted users,
 - `comments`: table of comments from registered users
    - `id`: reddit-specific ID of that comment
    - `author`: name of the user who made that comment
    - `score`: score of the comment
    - `permalink`: path to the comment in the web interface (not a full URL)
    - `sub`: name of the subreddit where the comment was made
    - `created`: UNIX timestamp of when the comment was first made
    - `body`: HTML-escaped textual content of the comment
 - `key_value`: key/value store that associates one key to many values
   for various operations of the bot that don't require their own table
    - `key`: key, often in the format "[feature]-[id]"
    - `value`: any string value
    - `created`: UNIX timestamp of when the key/value pair was added

## TODO

 1. post reports on a subreddit and keep them up to date for a little while
 1. database corrections from DTB's
 1. links previous/next in the web reports, and reports index
 1. backup discord messages
 1. ability to use multiple reddit accounts and proxies
 1. get data from pushshift.io
 1. replace blackfriday with snudown
 1. https support and auto renewal of certificates with letsencrypt
 1. wiki with discord authentication
 1. cleanup the config file
