package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebSession(t *testing.T) {
	t.Parallel()

	path := ":memory:"
	ctx := context.Background()

	_, conn, err := NewStorage(ctx, NewTestLevelLogger(t), StorageConf{Path: path, SecretsPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	wsf := WebSessionFactory{
		CookieName:   "session",
		MaxAge:       time.Hour,
		UpdateAfter:  time.Minute,
		MaxUpdateAge: 30 * time.Minute,
		MaxCSRFAge:   time.Minute,
	}

	id := "some id"

	var base *WebSession
	var session *WebSession
	var hErr *HTTPError

	t.Run("new session", func(t *testing.T) {
		w := httptest.NewRecorder()
		base, hErr = wsf.New(conn, w)
		if hErr != nil {
			t.Fatal(hErr)
		}

		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("invalid number of cookies: %d", len(cookies))
		}
		cookie := cookies[0]
		if cookie.Name != wsf.CookieName {
			t.Errorf("invalid cookie name %q, expected %q", cookie.Name, wsf.CookieName)
		} else if cookie.MaxAge != int(wsf.MaxAge.Seconds()) {
			t.Errorf("session cookie has invalid age: expected %d, got %d", wsf.MaxAge, cookie.MaxAge)
		}
	})

	t.Run("retrieve session from request", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{
			Name:  wsf.CookieName,
			Value: base.Token,
		})

		session, hErr = wsf.FromRequest(conn, w, r)
		if hErr != nil {
			t.Fatal(hErr)
		}

		if base.Token != session.Token {
			t.Errorf("expected token %q, got %q", base.Token, session.Token)
		} else if !base.Created.Truncate(time.Second).Equal(session.Created) {
			t.Errorf("expected creation time %s, got %s", base.Created, session.Created)
		} else if !base.Updated.Truncate(time.Second).Equal(session.Updated) {
			t.Errorf("expected update time %s, got %s", base.Updated, session.Updated)
		}
	})

	t.Run("session update", func(t *testing.T) {
		w := httptest.NewRecorder()

		prevUpdated := session.Updated
		session.Created = session.Created.Add(-2 * wsf.UpdateAfter)
		session.Updated = session.Updated.Add(-2 * wsf.UpdateAfter)

		if expired, hErr := session.Expire(conn, w); hErr != nil {
			t.Fatal(hErr)
		} else if expired {
			t.Errorf("session %s shouldn't be marked as expired", session)
		} else if !session.Updated.After(prevUpdated) {
			t.Errorf("update time of session %s should have been changed from %s", session, prevUpdated)
		}

		if cookies := w.Result().Cookies(); len(cookies) != 1 {
			t.Fatalf("invalid number of cookies: %d", len(cookies))
		} else if cookie := cookies[0]; cookie.MaxAge == -1 {
			t.Errorf("session cookie should not be deleted; got cookie %+v", cookie)
		}
	})

	t.Run("unauthorized ID", func(t *testing.T) {
		w := httptest.NewRecorder()
		if err := session.SetID(conn, w, id); err != nil {
			t.Log(err)
			if err.Code() != http.StatusForbidden {
				t.Errorf("should have gotten a forbidden error, got instead %v", err)
			}
		} else {
			t.Errorf("setting ID %q should have failed", id)
		}
	})

	t.Run("set authorized ID", func(t *testing.T) {
		w := httptest.NewRecorder()

		if err := conn.AddAuthorizedIDs([]string{id}); err != nil {
			t.Fatal(err)
		}

		if err := session.SetID(conn, w, id); err != nil {
			t.Error(err)
		}
		if session.ID != id {
			t.Error("session should also set on itself the new ID")
		}
	})

	t.Run("generate and check anti-CSRF token", func(t *testing.T) {
		w := httptest.NewRecorder()
		if err := session.NewCSRF(conn, w); err != nil {
			t.Fatal(err)
		}

		if !session.CSRFIsStillValid() {
			t.Errorf("session %s should still consider its anti-CSRF token %q to be valid", session, session.CSRF)
		}

		csrf := session.CSRF

		w = httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{
			Name:  wsf.CookieName,
			Value: session.Token,
		})

		session, hErr = wsf.FromRequest(conn, w, r)
		if hErr != nil {
			t.Fatal(hErr)
		}

		if session.CSRF != csrf {
			t.Errorf("the retrieved session %s should still hold the anti-CSRF token %q", session, csrf)
		} else if !session.CSRFIsStillValid() {
			t.Errorf("session %s should still consider its anti-CSRF token %q to be valid", session, csrf)
		}
	})

	t.Run("delete expired session", func(t *testing.T) {
		w := httptest.NewRecorder()
		t.Logf("%+v", wsf)
		t.Logf("%+v", session)
		if expired, hErr := session.Expire(conn, w); hErr != nil {
			t.Fatal(hErr)
		} else if expired {
			t.Errorf("session %s shouldn't be marked as expired yet", session)
		}

		session.Created = session.Created.Add(-(wsf.MaxAge + time.Second))

		if expired, hErr := session.Expire(conn, w); hErr != nil {
			t.Fatal(hErr)
		} else if !expired {
			t.Errorf("session %s should now be marked as expired", session)
		} else if cookies := w.Result().Cookies(); len(cookies) != 1 {
			t.Fatalf("invalid number of cookies: %d", len(cookies))
		} else if cookie := cookies[0]; cookie.MaxAge != -1 {
			t.Errorf("session cookie should have been deleted; got cookie %+v", cookie)
		}

		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{
			Name:  wsf.CookieName,
			Value: base.Token,
		})

		if noSession, hErr := wsf.FromRequest(conn, w, r); hErr != nil {
			t.Fatal(hErr)
		} else if noSession != nil {
			t.Errorf("session %s should have been deleted, instead found session %s", session, noSession)
		}
	})
}
