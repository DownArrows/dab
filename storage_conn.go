package main

import (
	"fmt"
	sqlite "github.com/bvinc/go-sqlite-lite/sqlite3"
	"time"
)

// StorageConn is a database connection from a specific Storage with application-specific methods to query the database.
// It implements SQLiteConn.
type StorageConn struct {
	actual SQLiteConn
}

/*****
 Users
******/

// Read

// GetUser fetches a User, within a UserQuery for easier use, from a case-insensitive name.
func (conn StorageConn) GetUser(username string) UserQuery {
	user := &User{}
	cb := func(stmt *SQLiteStmt) error { return user.FromDB(stmt) }
	err := conn.Select("SELECT * FROM users WHERE name = ? COLLATE NOCASE", cb, username)
	return UserQuery{
		Error:  err,
		Exists: user.Name != "",
		User:   *user,
	}
}

// ListUsers lists all users that are neither suspended nor deleted,
// ordered from the least recently scanned.
func (conn StorageConn) ListUsers() ([]User, error) {
	return conn.users("SELECT * FROM users WHERE suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

// ListSuspendedAndNotFound lists users that are either deleted or suspended, but not those that have been unregistered,
// ordered from the least recently scanned.
func (conn StorageConn) ListSuspendedAndNotFound() ([]User, error) {
	return conn.users("SELECT * FROM users WHERE suspended IS TRUE OR not_found IS TRUE ORDER BY last_scan")
}

// ListActiveUsers returns users that are considered active by the application,
// ordered from the least recently scanned.
func (conn StorageConn) ListActiveUsers() ([]User, error) {
	return conn.users("SELECT * FROM users WHERE inactive IS FALSE AND suspended IS FALSE AND not_found IS FALSE ORDER BY last_scan")
}

// ListRegisteredUsers returns all registered users, even if deleted or suspended, ordered from the most recently scanned.
func (conn StorageConn) ListRegisteredUsers() ([]User, error) {
	return conn.users("SELECT * FROM users ORDER BY last_scan DESC")
}

func (conn StorageConn) users(sql string) ([]User, error) {
	var users []User
	err := conn.Select(sql, func(stmt *SQLiteStmt) error {
		user := &User{}
		if err := user.FromDB(stmt); err != nil {
			return err
		}
		users = append(users, *user)
		return nil
	})
	return users, err
}

// Write

// UpdateInactiveStatus updates what is considered for a user to be "inactive",
// that is, if they haven't posted since maxAge.
func (conn StorageConn) UpdateInactiveStatus(maxAge time.Duration) error {
	sql := `
		WITH data AS (
			SELECT author FROM comments
			GROUP BY author HAVING (? - MAX(created)) > ?
		)
		UPDATE user_archive SET inactive = (name IN data)`
	return conn.Exec(sql, time.Now().Unix(), maxAge.Seconds())
}

// AddUser adds a User to the database. It doesn't check with Reddit, that is the responsibility of RedditUsers.
func (conn StorageConn) AddUser(query UserQuery) UserQuery {
	if query.User.Added.IsZero() {
		query.User.Added = time.Now()
	}
	user := query.User
	sql := "INSERT INTO user_archive(name, hidden, created, added, notes) VALUES (?, ?, ?, ?, ?)"
	query.Error = conn.Exec(sql, user.Name, user.Hidden, user.Created.Unix(), user.Added.Unix(), user.Notes)
	return query
}

// DelUser deletes a User that has the case-insensitive username.
func (conn StorageConn) DelUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET deleted = TRUE WHERE name = ? COLLATE NOCASE", username)
}

// UnDelUser undeletes a User that has the case-insensitive username.
func (conn StorageConn) UnDelUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET deleted = FALSE WHERE name = ? COLLATE NOCASE", username)
}

// HideUser hides a User that has the case-insensitive username.
func (conn StorageConn) HideUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET hidden = TRUE WHERE name = ? COLLATE NOCASE", username)
}

// UnHideUser un-hides a User that has the case-insensitive username.
func (conn StorageConn) UnHideUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET hidden = FALSE WHERE name = ? COLLATE NOCASE", username)
}

// SuspendUser sets a User as suspended (case-sensitive).
func (conn StorageConn) SuspendUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET suspended = TRUE WHERE name = ?", username)
}

// UnSuspendUser unsets a User as suspended (case-sensitive).
func (conn StorageConn) UnSuspendUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET suspended = FALSE WHERE name = ?", username)
}

// NotFoundUser sets a User as not found (case-sensitive), that is, seen from Reddit as deleted.
func (conn StorageConn) NotFoundUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET not_found = TRUE WHERE name = ?", username)
}

// FoundUser sets a User as found on Reddit (case-sensitive).
func (conn StorageConn) FoundUser(username string) error {
	return conn.simpleEditUser("UPDATE user_archive SET not_found = FALSE WHERE name = ?", username)
}

// PurgeUser completely removes the data associated with a User (case-insensitive).
func (conn StorageConn) PurgeUser(username string) error {
	return conn.simpleEditUser("DELETE FROM user_archive WHERE name = ? COLLATE NOCASE", username)
}

func (conn StorageConn) simpleEditUser(sql, username string) error {
	if err := conn.Exec(sql, username); err != nil {
		return err
	}
	if conn.Changes() == 0 {
		return fmt.Errorf("no user named %q", username)
	}
	return nil
}

/********
 Sessions
*********/

// AddAuthorizedIDs adds multiple IDs that are authorized to log into the web application.
// No error is returned for IDs that were already registered.
func (conn StorageConn) AddAuthorizedIDs(ids []string) error {
	return conn.WithTx(func() error {
		stmt, err := conn.Prepare("INSERT INTO secrets.authorized(id) VALUES (?) ON CONFLICT DO NOTHING")
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, id := range ids {
			if err := stmt.Exec(id); err != nil {
				return err
			}
			if err := stmt.ClearBindings(); err != nil {
				return err
			}
		}
		return nil
	})
}

// DelAuthorizedID removes an ID authorized to log into the web application.
func (conn StorageConn) DelAuthorizedID(id string) error {
	return conn.Exec("DELETE FROM secrets.authorized WHERE id = ?", id)
}

// AddSession registers a web session.
func (conn StorageConn) AddSession(token string, date time.Time) error {
	timestamp := date.Unix()
	return conn.Exec("INSERT INTO secrets.sessions(token, created, updated) VALUES (?, ?, ?)", token, timestamp, timestamp)
}

// GetSession retrieves a web session.
func (conn StorageConn) GetSession(token string) (*WebSession, error) {
	session := &WebSession{}
	if err := conn.Select("SELECT * FROM secrets.sessions WHERE token = ?", session.FromDB, token); err != nil {
		return nil, err
	}
	if session.Token == "" {
		return nil, nil
	}
	return session, nil
}

// CleanupSessions removes old web sessions and old anti-CSRF tokens.
func (conn StorageConn) CleanupSessions(wsf WebSessionFactory) error {
	now := time.Now()
	return conn.MultiExec([]SQLQuery{{
		SQL: "UPDATE secrets.sessions SET csrf = NULL, csrf_date = NULL WHERE csrf_date < ?",
		Args: []Any{
			now.Add(-wsf.MaxCSRFAge).Unix()},
	}, {
		SQL: "DELETE FROM secrets.sessions WHERE updated < ? OR created < ?",
		Args: []Any{
			now.Add(-wsf.MaxAge).Unix(),
			now.Add(-wsf.MaxUpdateAge).Unix()},
	}})
}

// UpdateSession updates the last use time of a web session and indicates if the session exists.
func (conn StorageConn) UpdateSession(token string, date time.Time) (exists bool, err error) {
	err = conn.Exec("UPDATE secrets.sessions SET updated = ? WHERE token = ?", date.Unix(), token)
	return (conn.Changes() > 0), err
}

// SetIDSession tries to register the ID of a web session, and returns if the session exists and if the ID is authorized.
func (conn StorageConn) SetIDSession(token, id string) (exists bool, authorized bool, err error) {
	err = conn.Exec("UPDATE secrets.sessions SET id = ? WHERE token = ?", id, token)
	if err != nil {
		if IsSQLiteForeignKeyError(err) {
			return false, false, nil
		}
		return false, false, err
	}
	return (conn.Changes() > 0), true, nil
}

// SetCSRFSession registers the CSRF token of a web session, and indicates if the session exists.
func (conn StorageConn) SetCSRFSession(token, csrf string, date time.Time) (exists bool, err error) {
	err = conn.Exec("UPDATE secrets.sessions SET csrf = ?, csrf_date = ? WHERE token = ?", csrf, date.Unix(), token)
	return (conn.Changes() > 0), err
}

// DelSession deletes a  web session.
func (conn StorageConn) DelSession(token string) error {
	return conn.Exec("DELETE FROM secrets.sessions WHERE token = ?", token)
}

/************
 Certificates
 ************/

// GetCertificate retrieves a certificate corresponding to the given key.
func (conn StorageConn) GetCertificate(key string) ([]byte, error) {
	var cert []byte
	var err error
	err = conn.Select("SELECT cert FROM certs WHERE key = ?", func(stmt *SQLiteStmt) error {
		cert, _, err = stmt.ColumnBlob(0)
		return err
	}, key)
	return cert, err
}

// AddCertificate registers a certificate with the given key.
func (conn StorageConn) AddCertificate(key string, cert []byte) error {
	return conn.Exec(`INSERT INTO certs VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, cert)
}

// DelCertificate deletes the certificate with the given key.
func (conn StorageConn) DelCertificate(key string) error {
	return conn.Exec("DELETE FROM certs WHERE key = ?", key)
}

/********
 Comments
*********/

// SaveCommentsUpdateUser saves comments of a single user, and updates the user's metadata according
// to the properties of the list of comments. It returns the updated User data structure to
// avoid having to do another request to get the update.
// What happens here controls how the scanner will behave next.
//
// Make sure the comments are all from the same user and the user struct is up to date.
// This method may seem to have a lot of logic for something in the storage layer,
// but most of it used to be in the scanner for reddit and outside of a transaction;
// putting the data-consistency related logic here simplifies greatly the overall code.
func (conn StorageConn) SaveCommentsUpdateUser(comments []Comment, user User, maxAge time.Duration) (User, error) {
	if user.Suspended {
		return user, conn.SuspendUser(user.Name)
	} else if user.NotFound {
		return user, conn.NotFoundUser(user.Name)
	}

	err := conn.WithTx(func() error {
		stmt, err := conn.Prepare(`
			INSERT INTO comments VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				score=excluded.score,
				body=excluded.body
			WHERE score != excluded.score OR body != excluded.body`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, comment := range comments {
			if err := stmt.Exec(comment.ToDB()...); err != nil {
				return err
			}
			if err := stmt.ClearBindings(); err != nil {
				return err
			}
		}

		// Frow now on we don't need to check for an error because if the user doesn't exist,
		// then the constraints would have made the previous statement fail.

		// We need to know how many relevant we got, and save it in the user's metadata.
		// This way, the scanner can avoid fetching superfluous comments.
		user.BatchSize = 0
		for _, comment := range comments {
			if time.Now().Sub(comment.Created) < maxAge {
				user.BatchSize++
			}
		}

		if user.New && user.Position == "" { // end of the listing reached
			if err := conn.Exec("UPDATE user_archive SET new = FALSE WHERE name = ?", user.Name); err != nil {
				return err
			}
		}

		if !user.New && user.BatchSize < uint(len(comments)) { // position resetting doesn't apply to new users
			user.Position = ""
		}

		// All comments are younger than maxAge, there may be more.
		if user.BatchSize == uint(len(comments)) {
			user.BatchSize = MaxRedditListingLength
		}

		user.LastScan = time.Now()

		sql := "UPDATE user_archive SET position = ?, batch_size = ?, last_scan = ? WHERE name = ?"
		return conn.Exec(sql, user.Position, int(user.BatchSize), user.LastScan.Unix(), user.Name)
	})

	return user, err
}

// GetCommentsBelowBetween returns the comments below a score, between since and until.
// To be used within a transaction.
func (conn StorageConn) GetCommentsBelowBetween(score int64, since, until time.Time) ([]Comment, error) {
	return conn.comments(`
			SELECT comments.*
			FROM users JOIN comments
			ON comments.author = users.name
			WHERE
				comments.score <= ?
				AND users.hidden IS FALSE
				AND comments.created BETWEEN ? AND ?
			ORDER BY comments.score ASC
		`, score, since.Unix(), until.Unix())
}

// Comments returns the most downvoted comments, up to a number set by the limit, with an offset.
func (conn StorageConn) Comments(page Pagination) ([]Comment, error) {
	return conn.comments(`
			SELECT comments.*
			FROM users JOIN comments
			ON comments.author = users.name
			WHERE
				comments.score < 0
				AND users.hidden IS FALSE
			ORDER BY score ASC LIMIT ? OFFSET ?
		`, int(page.Limit), int(page.Offset))
}

// UserComments returns the most downvoted comments of a single User, up to a number set by limit, and skipping a positive number.
func (conn StorageConn) UserComments(username string, page Pagination) ([]Comment, error) {
	sql := "SELECT * FROM comments WHERE author = ? ORDER BY score ASC LIMIT ? OFFSET ?"
	return conn.comments(sql, username, int(page.Limit), int(page.Offset))
}

func (conn StorageConn) comments(sql string, args ...Any) ([]Comment, error) {
	var comments []Comment
	cb := func(stmt *SQLiteStmt) error {
		comment := &Comment{}
		if err := comment.FromDB(stmt); err != nil {
			return err
		}
		comments = append(comments, *comment)
		return nil
	}
	err := conn.Select(sql, cb, args...)
	return comments, err
}

// SaveAndDiffHighScores saves and returns comments that were new.
func (conn StorageConn) SaveAndDiffHighScores(comments []Comment) ([]Comment, error) {
	var diff []Comment
	err := conn.WithTx(func() error {
		stmt, err := conn.Prepare("INSERT INTO highscores(id) VALUES (?)")
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, comment := range comments {

			if err := stmt.Exec(comment.ID); err != nil {
				if !IsSQLitePrimaryKeyConstraintError(err) {
					return err
				}
			} else {
				diff = append(diff, comment)
			}

			if err := stmt.ClearBindings(); err != nil {
				return err
			}

		}

		return nil
	})
	return diff, err
}

/**********
 Statistics
***********/

// GetKarma returns the total and negative karma of a User (case-insensitive).
func (conn StorageConn) GetKarma(username string) (int64, int64, error) {
	sql := `
		SELECT SUM(score), SUM(CASE WHEN score < 0 THEN score ELSE NULL END)
		FROM comments WHERE author = ? COLLATE NOCASE`
	var total int64
	var negative int64
	var err error
	cb := func(stmt *SQLiteStmt) error {
		// In both cases assume 0 if NULL, as it's not really
		// useful to gracefully handle this corner case.
		total, _, err = stmt.ColumnInt64(0)
		if err != nil {
			return err
		}
		negative, _, err = stmt.ColumnInt64(1)
		return err
	}
	err = conn.Select(sql, cb, username)
	return total, negative, err
}

// StatsBetween returns the commenting statistics of all non-hidden users below a score, between since and until.
// To be used within a transaction.
func (conn StorageConn) StatsBetween(since, until time.Time) (StatsCollection, error) {
	return conn.selectStats(StatsRead{Name: true}, `
		SELECT
			COUNT(comments.id),
			SUM(comments.score) AS total,
			AVG(comments.score),
			comments.author
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE
			comments.score < 0
			AND users.hidden IS FALSE
			AND comments.created BETWEEN ? AND ?
		GROUP BY comments.author
		ORDER BY total`, since.Unix(), until.Unix())
}

// CompendiumPerUser returns the per-user statistics of all users, for use with the compendium.
func (conn StorageConn) CompendiumPerUser() (StatsCollection, StatsCollection, error) {
	return conn.compendiumSelectStats(`
		SELECT
		/* All comments */
			COUNT(comments.id),
			SUM(comments.score) AS karma,
			AVG(comments.score),
			comments.author,
			MAX(comments.created),
		/* Only negative comments */
			COUNT(CASE WHEN comments.score < 0 THEN 1 ELSE NULL END),
			SUM(CASE WHEN comments.score < 0 THEN comments.score ELSE NULL END),
			AVG(CASE WHEN comments.score < 0 THEN comments.score ELSE NULL END),
			MAX(CASE WHEN comments.score < 0 THEN comments.created ELSE NULL END)
		FROM users JOIN comments
		ON comments.author = users.name
		WHERE users.hidden IS FALSE
		GROUP BY author
		ORDER BY karma ASC`)
}

// CompendiumUserPerSub returns the commenting statistics for a single user, for use with the compendium.
func (conn StorageConn) CompendiumUserPerSub(username string) (StatsCollection, StatsCollection, error) {
	return conn.compendiumSelectStats(`
		SELECT
		/* All comments */
			COUNT(score), SUM(score) AS karma, AVG(score), sub, MAX(created),
		/* Only negative comments */
			COUNT(CASE WHEN score < 0 THEN 1 ELSE NULL END),
			SUM(CASE WHEN score < 0 THEN score ELSE NULL END),
			AVG(CASE WHEN score < 0 THEN score ELSE NULL END),
			MAX(CASE WHEN score < 0 THEN created ELSE NULL END)
		FROM comments WHERE author = ?
		GROUP BY sub
		ORDER BY karma ASC`, username)
}

func (conn StorageConn) compendiumSelectStats(sql string, args ...Any) (StatsCollection, StatsCollection, error) {
	var all StatsCollection
	var negative StatsCollection

	allRead := StatsRead{Name: true, Latest: true}
	negRead := StatsRead{Start: 5, Latest: true}

	cb := func(stmt *SQLiteStmt) error {
		allStats := &Stats{}
		negStats := &Stats{}
		if err := allStats.FromDB(stmt, allRead); err != nil {
			return err
		}
		all = append(all, *allStats)

		if err := negStats.FromDB(stmt, negRead); err != nil {
			return err
		}
		negStats.Name = allStats.Name
		negative = append(negative, *negStats)

		return nil
	}

	err := conn.Select(sql, cb, args...)
	return all, negative, err
}

func (conn StorageConn) selectStats(statsRead StatsRead, sql string, args ...Any) (StatsCollection, error) {
	var data StatsCollection
	cb := func(stmt *SQLiteStmt) error {
		stats := &Stats{}
		if err := stats.FromDB(stmt, statsRead); err != nil {
			return err
		}
		data = append(data, *stats)
		return nil
	}
	err := conn.Select(sql, cb, args...)
	return data, err
}

/*************************
 SQLiteConn implementation
**************************/

// Lock implements SQLiteConn.
func (conn StorageConn) Lock() {
	conn.actual.Lock()
}

// Unlock implements SQLiteConn.
func (conn StorageConn) Unlock() {
	conn.actual.Unlock()
}

// Analyze implements SQLiteConn.
func (conn StorageConn) Analyze() error {
	return conn.actual.Analyze()
}

// LastAnalyze implements SQLiteConn.
func (conn StorageConn) LastAnalyze() time.Time {
	return conn.actual.LastAnalyze()
}

// Base implements SQLiteConn.
func (conn StorageConn) Base() *sqlite.Conn {
	return conn.actual.Base()
}

// Path implements SQLiteConn.
func (conn StorageConn) Path() string {
	return conn.actual.Path()
}

// Close implements SQLiteConn.
func (conn StorageConn) Close() error {
	return conn.actual.Close()
}

// Changes implements SQLiteConn.
func (conn StorageConn) Changes() int {
	return conn.actual.Changes()
}

// ReadUncommitted implements SQLiteConn.
func (conn StorageConn) ReadUncommitted(set bool) error {
	return conn.actual.ReadUncommitted(set)
}

// Backup implements SQLiteConn.
func (conn StorageConn) Backup(srcName string, destConn SQLiteConn, destName string) (*SQLiteBackup, error) {
	return conn.actual.Backup(srcName, destConn, destName)
}

// Prepare implements SQLiteConn.
func (conn StorageConn) Prepare(sql string, args ...Any) (*SQLiteStmt, error) {
	return conn.actual.Prepare(sql, args...)
}

// Select implements SQLiteConn.
func (conn StorageConn) Select(sql string, cb func(*SQLiteStmt) error, args ...Any) error {
	return conn.actual.Select(sql, cb, args...)
}

// Exec implements SQLiteConn.
func (conn StorageConn) Exec(sql string, args ...Any) error {
	return conn.actual.Exec(sql, args...)
}

// MultiExec implements SQLiteConn.
func (conn StorageConn) MultiExec(queries []SQLQuery) error {
	return conn.actual.MultiExec(queries)
}

// WithTx implements SQLiteConn.
func (conn StorageConn) WithTx(cb func() error) error {
	return conn.actual.WithTx(cb)
}

// MultiExecWithTx implements SQLiteConn.
func (conn StorageConn) MultiExecWithTx(queries []SQLQuery) error {
	return conn.actual.MultiExecWithTx(queries)
}
