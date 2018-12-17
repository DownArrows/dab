# Down Arrows Bot

This bot scans some reddit users, interacts with a discord server of your choice, and build reports about their most downvoted comments.
It is supposed to run continuously, typically on a server, and was designed to use as few resources as possible.

## End user manual

### Web interface

Reports are accessible at `/reports/<year>/<week number>`, `/reports/current` and `/reports/lastweek`.

### Discord commands

Commands must start with the configured prefix (defaults to `!`), and if they take arguments, must be separated from them by a single white space.
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
 - `sep` or `separator` or `=` post a separation rule

If you post a link in the main channel that contains a link to a comment on reddit,
it will be reposted on the log channel if the administrator of the bot has enabled this feature.
To prevent that, add the command `nolog` anywhere in your comment.

### Database

The database is an SQLite file.
You can use a file identification tool (like `file` on UNIX-like OSes) to get information about it.
It has the application ID `3499` (or `dab` in hexadecimal),
and the version of the bot that last wrote into the database is encoded in the "user version" field.
You can check an SQLite file is a valid DAB database and get its version with the following python (2 and 3) script:

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
			  msg = "'{path}' has incorrect application id {id} (expected 0xdab)."
			  print(msg.format(path=db_path, id=app_id))
			  exit(1)
		 f.seek(60) # user version
		 v = bytearray(f.read(4))
		 msg = "File last written by DAB version {major}.{minor}.{bugfix}."
		 print(msg.format(major=v[1], minor=v[2], bugfix=v[3]))

Save this script in a file, for example named `dab_db.py`, and run it on `dab.db` with `python dab_db.py dab.db`.


If you want to browse the database, you can use something like the [DB Browser for SQLite](https://sqlitebrowser.org/).
Here is the explanation of each table and their columns:

 - `user_archive`: table of all registered reddit users, deleted or not
    - `name`: name of the user
    - `created`: UNIX timestamp of the creation date according to reddit
    - `not_found`: 1 if trying to get information about this user resulted in a 404 not found error
    - `suspended`: 1 if suspended according to reddit's API
    - `added`: UNIX timestamp of the date when the user was added to the database
    - `batch_size`: Number of comments below the max age on the last scan
    - `deleted`: 1 if user is marked as deleted (will not be scanned or included in reports anymore)
    - `hidden`: 1 if user is scanned but not shown in reports
    - `inactive`: 1 if considered inactive
    - `last_scan`: UNIX timestamp of the last time this user was scanned
    - `new`: 1 until all reachable pages of comments of that user have been saved
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
 - `seen_posts`: basic information about posts that have already been seen by the bot (avoids repeated actions)
    - `id`: reddit ID of the post
    - `sub`: name of the subreddit where the post was made
    - `created`: UNIX timestamp of when the post was made
 - `known_objects`: set of various values to persist for specific purposes
    - `id`: identifier or content of the thing
    - `date`: when this thing was added

## Administrator manual

DAB has only been compiled and used on GNU/Linux so far.
It should theoretically work on other platforms.

The configuration is organized around three main components — discord, reddit, the web server — that can be activated independently.
You can only launch the web server on an already populated database, or only scan reddit and don't connect to discord.
See the section on the available configuration options to see what is necessary to enable each component.

### Compiling

You need at least Go 1.11, which you can download at <https://golang.org/dl/>, as well as git, the gcc compiler, and the headers of a C library.
On Debian-based distributions you can get all those dependencies with `apt install golang gcc libc-dev git`.
The version of Go may not be high enough, in which case download it from the official website and install it manually.
See <https://golang.org/cmd/go/#hdr-Environment_variables> if you have specific needs.
Once installed, go into the source folder, run `go get -d` to download the dependencies, and then `go build` (that may take a few minutes the first time).

### Reddit and Discord credentials

For the Discord component, you first need an account,
then go on https://discordapp.com/developers/, create an application, add a bot to it, and get its client ID and token.
Then put the token into DAB's configuration file, and substitute `CLIENT_ID` for the actual one in the following URL:
`https://discordapp.com/oauth2/authorize?client_id=CLIENT_ID&scope=bot&permissions=0`.
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
Once you got a client ID and a secret, put the account's username, its password, the client ID and the secret inside the configuration file.

### Running and maintenance

To run it simply call the binary. It will expect a file named `dab.conf.json` in the current directory.
To use another path for the configuration file use `dab -config /your/custom/path/dab.conf.json` (note that the file can have any name).
It needs a valid configuration file and to be able to open the database it is configured with (defaults to `dab.db` in the current directory).

If you want to run it with [systemd](https://en.wikipedia.org/wiki/Systemd), here is a sample unit file to get you started:

	[Unit]
	Description=DAB (Down Arrow Bot)
	After=network.target

	[Install]
	WantedBy=multi-user.target

	[Service]
	ExecStart=/usr/local/bin/dab -config /etc/dab.conf.json
	Restart=on-failure

To backup the database, **do not** copy the file it opens (given in the `database` section of the config file, option `path`).
Only use the built-in backup system by downloading the file with HTTP (which must be enabled in the `web` section by setting `listen`).
It serves a cached backup if it is not too old (`backup_max_age` in the configuration file), and otherwise creates one before sending it.
This can be used in a backup script called by cron, like so:

	#!/bin/sh
	set -e
	src="/var/lib/dab/db.sqlite3"
	bak="/srv/dab/dab.db.bak"
	wget -O"$bak" -q http://localhost:12345/backup
	rsync -e "ssh -i /root/.ssh/backup" "$bak" backup@anothercomputer:/var/backups/dab.sqlite3

If after the bot has stopped there are files ending in `-shm`, `-wal` and `-journal` in the folder containing the database file,
**do not** delete them, they probably contain data and deleting them could leave the database corrupted.
Instead leave them with the database, then re-run and stop the bot normally, or if you just want to repair the database, run it with the `-initdb` option.
Those files are also present when the bot is running, which is perfectly normal.
For more information about them see <https://sqlite.org/tempfiles.html>.

### Command line interface

Most of the configuration happens in the configuration file.
The command line interface only affects the overall behavior of the program:

 - `-config` Path to the configuration file. Defaults to `./dab.conf.json`
 - `-help` Print the help for command line interface.
 - `-initdb` Initialize the database and exit.
 - `-report` Print the report for last week on the standard output and exit.
 - `-useradd` Add one or multiple usernames to be tracked and exit.

### Configuration

The configuration file is a JSON file whose top-level data container is a dictionary.
The list below shows every option, where a sub-list corresponds to a dictionary,
and each option follows the pattern `<option's key> <type> (<default>): <explanation>`.
All keys *must* be in lower case.

There are two application-specific types: timezone and duration.
They are JSON strings validated and interpreted according to
<http://golang.localhost/pkg/time/#ParseDuration> and <http://golang.localhost/pkg/time/#LoadLocation>.
For Go templates' syntax, see <http://golang.localhost/pkg/text/template/>.

 - `hide_prefix` *string* (hide/): prefix you can add to usernames to hide them from reports (used by `-useradd` and on Discord)
 - `timezone` *timezone* (UTC): timezone used to format dates and compute weeks and years
 - `database`
    - `backup_max_age` *duration* (24h): if the backup is older than that when a backup is requested, the backup will be refreshed; must be at least one hour
    - `backup_path` *string* (./dab.db.backup): path to the backup of the database
    - `cleanup_interval` *duration* (*none*): interval between clean-ups of the database (reduces its size and optimizes queries);
       leave out to disable, else must be at least one minute
    - `path` *string* (./dab.db): path to the database file
 - `discord`
    - `admin` *string* (*none*): Discord ID of the privileged user (use Discord's developer mode to get them);
      if empty will use the owner of the channels' server, and if no channel is enabled, will disable privileged commands
    - `general` *string* (*none*): Discord ID of the main channel where loggable links are taken from and welcome messages are posted;
      required to have welcome messages and logged links, disabled if left empty
    - `hide_prefix` *string* (*none*): Discord-specific hide prefix when registering users (overrides the global hide prefix)
    - `highscores` *string* (*none*): Discord ID of the channel where links to high-scoring comments are posted; disabled if left empty
    - `highscore_threshold` *int* (-1000): score at and below which a comment will be linked to in the highscore channel
    - `log` *string* (*none*): Discord ID of the channel where links to comments on reddit are reposted; disabled if left empty
    - `prefix` *string* (!): prefix for commands
    - `token` *string* (*none*): token to connect to Discord; leave out to disable the Discord component
    - `welcome` *string* (*none*): Go template of the welcome message; it is provided with two top-level keys,
      `ChannelsID` and `Member`. `ChannelsID` provides `General`, `Log` and `HighScores`, which contains the numeric ID of those channels.
      `Member` provides `ID`, `Name`, and `FQN` (name followed by a discriminator). Disables welcome messages if left empty
 - `reddit`
    - `compendium_update_interval` *duration* (*none*): interval between each scan of the compendium; leave out to disable, else must be at least an hour
    - `dvt_interval` *string* (*none*): interval between each check of the downvote sub's new reports; leave out to disable, else must be at least a minute
    - `full_scan_interval` *duration* (6h): interval between each scan of all users, inactive or not
    - `id` *string* (*none*): Reddit application ID for the bot (required for users' scanning)
    - `inactivity_threshold` *duration* (2200h): if a user hasn't commented since that long ago, consider them "inactive" and scan them less often;
      must be at least one day
    - `max_age` *duration* (24h): don't get more batches of an user's comments if the oldest comment found is older than that; must be at least one day
    - `max_batches` *int* (5): maximum number of batches of comments to get from Reddit for a single user before moving to the next one
    - `password` *string* (*none*): Reddit password for the bot's account (required for users' scanning)
    - `secret` *string* (*none*): Reddit application secret for the bot (required for users' scanning)
    - `unsuspension_interval` *duration* (*none*): interval between each batch of checks for suspended or deleted users;
      leave out to disable, else must be at least one minute
    - `user_agent` *string* (*none*): Go template for the user agent of the bot on reddit; `OS` and `Version` are provided (required for users' scanning)
    - `username` *string* (*none*): Reddit username for the bot's account (required for users' scanning)
 - `report`
    - `cutoff` *int* (-50): ignore comments whose score is higher than this
    - `leeway` *duration* (12h): shift back the time window for comments' inclusion in the report to include those that were made late; cannot be negative
    - `nb_top` *int* (5): maximum number of users to include in the list of statistics for the report
 - `web`
    - `listen` *string* (*none*): `hostname:port` or `ip:port` or `:port` (all interface) specification for the webserver to listen to; leave out to disable

### Sample configuration

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
			"max_age":                    "72h",
			"dvt_interval":               "5m",
			"unsuspension_interval":      "15m",
			"compendium_update_interval": "1h"
		},

		"discord": {
			"token":      "NJx4MJt5ODt5MTk0MzM2Mjc6.DrQECx.pMN84B0UfL2WxxssdxHxx0MxxK8",
			"general":    "508169151894056221",
			"log":        "508869940013213344",
			"highscores": "508263683211452578",
			"welcome":    "Hello <@{{.Member.ID}}>!"
		},

		"web": {
			"listen": "localhost:12345"
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
 - type struct fields and methods names in camel case,
   but only capitalize the first letter if they are to be used outside of the struct's methods and creation function(s)
   (we treat unexported fields and methods like private methods and attributes in object-oriented programming,
   although since everything is in the same package at this point it's nothing more than a convention that isn't enforced by the compiler)
 - sort by name struct fields, or if they are really about different things, separate them with a blank line
 - use snake case for variables inside functions
 - use camel case for constants, types and variables at package-level
 - start names at package-level with a capitalized letter only if they are used in another file
 - unless you add a lot of code, keep everything inside the main package (having to deal with multiple packages complicates things)
 - lay out files in that order, unless there is no clear central type,
 in which case just do whatever makes the most sense when reading from top to bottom:
    1. constants, variables, the init function if any
    2. types used in other files
    3. file-specific types followed by the function to create them and then their methods
    4. the central type, followed by the function to create it, then the methods
 - for data structures that are used by several other parts of the code but for different sets of methods, define an interface for each usage of the data structure so that one can know at a glance who uses what

### Architecture

This application has several components that run independently and communicate either through the database layer or through channels.
They are managed by the `DownArrowsBot` data structure in `dab.go`;
it checks what the user wants to do, what components can be launched according to the configuration file,
propagates the root context for proper cancellation, and waits for each component to shut down and returns errors.
It also logs what components are enabled and why some are disabled.
Its main tool to achieve that is the `TaskGroup` data structure (in `concurrency.go`),
which allows to manage groups of functions launched as goroutines.
Those functions must have the following type signature: `func (context.Context) error`.
If the function you want to manage with a task group has a different type signature, wrap it in an anonymous function.

A component is a data structure which has one or several methods that take at least a context and return an error.
See the `DownArrowsBot` data structure definition in `dab.go` for the list of components.
Since goroutines that write to channels would block if the goroutines reading the channels were to be stopped before them,
a task group only for writers is used and stopped before the task group for readers, allowing for proper and orderly shutdown.
Make sure that any function spawned by a task group properly returns if it receives a cancellation signal at any point
(it may or may not return the cancellation error, it doesn't matter).

`Storage` is not a component, it is a layer, and is passed by reference to other data structures.
Since its methods panic on most errors (because most errors would mean a serious issue with the database, requiring to immediately stop),
it must be closed last, after all components have been stopped.
Its instantiation shouldn't panic though, it needs to properly report errors and close the database.

### TODO

 - launch the web server while connecting to reddit and discord
 - retry connecting to reddit and discord
 - auto post reports to a specific sub and maintain its wiki
 - better browsability of the web reports
 - replace blackfriday with snudown
