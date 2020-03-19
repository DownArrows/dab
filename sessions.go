package main

import (
	"fmt"
	"net/http"
	"time"
)

// WebSessionFactory configures, creates, and retrieves web sessions.
// It is made to be used within an  HTTP server,
// and therefore takes HTTP response and request objects, and returns *HTTPError.
type WebSessionFactory struct {
	CookieName   string
	MaxAge       time.Duration
	UpdateAfter  time.Duration
	MaxUpdateAge time.Duration
	MaxCSRFAge   time.Duration
}

// NewWebSessionFactory creates a WebSessionFactory from a cookie name and a given configuration.
func NewWebSessionFactory(name string, conf WebSessionConf) WebSessionFactory {
	return WebSessionFactory{
		CookieName:   name,
		MaxAge:       conf.MaxAge.Value,
		UpdateAfter:  conf.UpdateAfter.Value,
		MaxUpdateAge: conf.MaxUpdateAge.Value,
		MaxCSRFAge:   conf.MaxCSRFAge.Value,
	}
}

// New returns a new web session.
func (wsf WebSessionFactory) New(conn StorageConn, w http.ResponseWriter) (*WebSession, *HTTPError) {
	now := time.Now()

	token, err := NewRandomToken()
	if err != nil {
		return nil, NewHTTPErrorf(http.StatusInternalServerError, "failed to generate a session token: %v", err)
	}

	session := &WebSession{
		WebSessionFactory: wsf,
		Token:             token,
		Created:           now,
		Updated:           now,
	}

	if err := conn.AddSession(session.Token, now); err != nil {
		return nil, NewHTTPErrorf(http.StatusInternalServerError, "failed to save session information: %v", err)
	}

	session.SetCookie(w)
	return session, nil
}

// FromRequest retrieves a web session from an HTTP request.
func (wsf WebSessionFactory) FromRequest(conn StorageConn, w http.ResponseWriter, r *http.Request) (*WebSession, *HTTPError) {
	cookie, err := r.Cookie(wsf.CookieName)
	if err == http.ErrNoCookie {
		return nil, nil
	} else if err != nil {
		DeleteCookie(w, cookie)
		return nil, NewHTTPError(http.StatusBadRequest, err)
	}

	session, err := conn.GetSession(cookie.Value)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, err)
	} else if session == nil {
		DeleteCookie(w, cookie)
		return nil, nil
	}
	session.WebSessionFactory = wsf

	session.SetCookie(w)
	return session, nil
}

// WebSession represents a session for the web, and is supposed to be used for log-in.
// Its methods manage at the same time the struct, the database, and the HTTP client's cookie.
// On every SQL request the session is checked to see if it is still valid.
// Every method that takes a database connection should be run inside a database transaction.
type WebSession struct {
	WebSessionFactory
	Token    string
	ID       string
	Created  time.Time
	Updated  time.Time
	CSRF     string
	CSRFDate time.Time
}

// InitializationQueries returns the required SQL queries for web sessions to work.
func (WebSession) InitializationQueries() []SQLQuery {
	return []SQLQuery{
		{SQL: `CREATE TABLE IF NOT EXISTS secrets.sessions(
			token TEXT NOT NULL PRIMARY KEY,
			id TEXT,
			created INTEGER NOT NULL,
			updated INTEGER NOT NULL,
			csrf TEXT,
			csrf_date INTEGER,
			FOREIGN KEY (id) REFERENCES authorized(id) ON DELETE CASCADE
		) WITHOUT ROWID`},
		{SQL: `CREATE TABLE IF NOT EXISTS secrets.authorized(
			id TEXT PRIMARY KEY
		) WITHOUT ROWID`},
	}
}

// FromDB reads a web session from an SQL query that selects all the fields in the table of sessions.
func (session *WebSession) FromDB(stmt *SQLiteStmt) error {
	var err error

	if session.Token, _, err = stmt.ColumnText(0); err != nil {
		return err
	}

	if session.ID, _, err = stmt.ColumnText(1); err != nil {
		return err
	}

	var timestamp int64
	if timestamp, _, err = stmt.ColumnInt64(2); err != nil {
		return err
	}
	session.Created = time.Unix(timestamp, 0)

	if timestamp, _, err = stmt.ColumnInt64(3); err != nil {
		return err
	}
	session.Updated = time.Unix(timestamp, 0)

	if session.CSRF, _, err = stmt.ColumnText(4); err != nil {
		return err
	}

	if timestamp, _, err = stmt.ColumnInt64(5); err != nil {
		return err
	}
	session.CSRFDate = time.Unix(timestamp, 0)

	return nil
}

// String returns a representation of the session that could be shown to the client if there is an error.
func (session *WebSession) String() string {
	return fmt.Sprintf("%q created at %s and last updated at %s", session.Token, session.Created, session.Updated)
}

// Expire checks and returns whether the session has expired, deletes it if it did, or refreshes the field Updated.
func (session *WebSession) Expire(conn StorageConn, w http.ResponseWriter) (expired bool, err *HTTPError) {
	now := time.Now()
	diffUpdated := now.Sub(session.Updated)

	if diffUpdated > session.MaxUpdateAge || now.Sub(session.Created) > session.MaxAge {
		if err := session.del(conn, w); err != nil {
			return false, err
		}
		return true, nil
	}

	if diffUpdated > session.UpdateAfter {
		if exists, err := conn.UpdateSession(session.Token, now); err != nil {
			return false, NewHTTPErrorf(http.StatusInternalServerError, "error when updating session %s: %v", session, err)
		} else if !exists {
			return true, nil
		}
		session.Updated = now
		session.SetCookie(w)
	}

	return false, nil
}

// SetID sets the ID of the session; a ready-to-use error is returned if the ID isn't authorized.
func (session *WebSession) SetID(conn StorageConn, w http.ResponseWriter, id string) *HTTPError {
	if exists, authorized, err := conn.SetIDSession(session.Token, id); err != nil {
		return NewHTTPErrorf(http.StatusInternalServerError, "error when registering your ID: %v", err)
	} else if !authorized {
		return NewHTTPErrorf(http.StatusForbidden, "ID %q isn't allowed to access the application", id)
	} else if !exists {
		return session.errDoesNotExist(w)
	}
	session.ID = id
	session.SetCookie(w)
	return nil
}

// NewCSRF generates a new anti-CSRF token, cloberring any previous one.
func (session *WebSession) NewCSRF(conn StorageConn, w http.ResponseWriter) *HTTPError {
	csrf, err := NewRandomToken()
	if err != nil {
		return NewHTTPErrorf(http.StatusInternalServerError, "session %s failed to generate an anti-CSRF token: %v", session, err)
	}
	now := time.Now()
	if exists, err := conn.SetCSRFSession(session.Token, csrf, now); err != nil {
		return NewHTTPErrorf(http.StatusInternalServerError, "failed to update session %s: %v", session, err)
	} else if !exists {
		return session.errDoesNotExist(w)
	}
	session.CSRF = csrf
	session.CSRFDate = now
	return nil
}

// CSRFIsStillValid tells whether the CSRF token is stil valid according to its creation time.
// We don't test the anti-CSRF token directly so as to allow for custom comparisons (eg. through hashes)
func (session *WebSession) CSRFIsStillValid() bool {
	return time.Now().Sub(session.CSRFDate) <= session.MaxCSRFAge
}

// SetCookie sets the cookie corresponding to the session.
func (session *WebSession) SetCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		HttpOnly: true,
		MaxAge:   int(session.MaxAge.Seconds()),
		Name:     session.CookieName,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
		Value:    session.Token,
	})
}

func (session *WebSession) del(conn StorageConn, w http.ResponseWriter) *HTTPError {
	if err := conn.DelSession(session.Token); err != nil {
		return NewHTTPError(http.StatusInternalServerError, err)
	}
	session.MaxAge = -1 * time.Second
	session.SetCookie(w)
	return nil
}

func (session *WebSession) errDoesNotExist(w http.ResponseWriter) *HTTPError {
	session.MaxAge = -1 * time.Second
	session.SetCookie(w)
	return NewHTTPErrorf(http.StatusForbidden, "unknown session %s", session)
}
