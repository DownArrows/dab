#!/usr/bin/env python3
import argparse
import configparser
import logging
import math
import sqlite3
import sys
import time
from contextlib import suppress

import praw


sqlite3.register_adapter(bool, int)
sqlite3.register_converter("BOOLEAN", lambda value: bool(int(value)))


class RDACB:

    reddit = None
    db = None

    def __init__(self, client_conf, db_name, log_level=logging.WARNING):
        self.logger = logging.getLogger("rdacb")
        self.logger.setLevel(log_level)

        self.reddit = praw.Reddit(user_agent=client_conf["user_agent"],
                                  client_id=client_conf["client_id"],
                                  client_secret=client_conf["client_secret"])
        self.db = sqlite3.connect(db_name)

    def init_db(self):
        with self.db:
            self.db.execute("""
                CREATE TABLE IF NOT EXISTS tracked (
                    username TEXT PRIMARY KEY,
                    added INTEGER NOT NULL,
                    hidden BOOLEAN DEFAULT 0,
                    deleted BOOLEAN DEFAULT 0 
                )""")
            self.db.execute("""
                CREATE TABLE IF NOT EXISTS downvoted (
                    id TEXT PRIMARY KEY,
                    author TEXT NOT NULL,
                    score INTEGER NOT NULL,
                    permalink TEXT NOT NULL,
                    sub_id TEXT NOT NULL,
                    created INTEGER NOT NULL,
                    body TEXT NOT NULL,
                    FOREIGN KEY (author) REFERENCES tracked(username)
                ) WITHOUT ROWID""")
        self.logger.info("Database initialized.")

    def add_user(self, user, hidden=False):
        with self.db:
            self.logger.debug(f"Adding user {user}.")
            self.db.execute("INSERT INTO tracked VALUES (?, ?, ?, ?)",
                            (user, math.trunc(time.time()), hidden, False))
        self.logger.debug(f"User {user} successfully added.")

    def get_users(self):
        query = "SELECT username, hidden FROM tracked WHERE deleted = 0"
        return self.db.execute(query).fetchall()

    def scan_user(self, user):
        self.logger.info(f"Scanning user '{user}'.")
        for comment in self.reddit.redditor(user).comments.new():
            self.logger.info(f"Saving comment {comment.id} from user '{user}'.")
            if not comment.score_hidden:
                self.save_comment(user, comment)

    def save_comment(self, author, comment):
        with self.db:
            data = (comment.fullname, author, comment.score, comment.permalink,
                    comment.subreddit_id, comment.created_utc,
                    comment.body)
            query = "INSERT OR REPLACE INTO downvoted VALUES (?, ?, ?, ?, ?, ?, ?)"
            self.db.execute(query, data)

    def run(self):
        while True:
            users = self.get_users()
            if len(users) == 0:
                self.logger.info("No user to scan found.")
                time.sleep(10)
                continue
            self.logger.info(f"Scanning users {users}.")
            for (user, hidden) in self.get_users():
                self.scan_user(user)


if __name__ == "__main__":
    config = configparser.ConfigParser()
    config.read(sys.argv[1])
    logging.basicConfig(level=logging.DEBUG)

    bot = RDACB(config["Client"], config["Database"]["path"],
                log_level=logging.DEBUG)
    bot.init_db()
    bot.run()
