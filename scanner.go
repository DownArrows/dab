package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const accessTokenURL = "https://www.reddit.com/api/v1/access_token"

const requestBaseURL = "https://oauth.reddit.com"

type RedditAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Id       string `json:"id"`
	Secret   string `json:"secret"`
}

type OAuthResponse struct {
	Token   string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Type    string `json:"token_type"`
	Timeout int    `json:"expires_in"`
	Scope   string `json:"scope"`
}

type Scanner struct {
	sync.Mutex
	Client    *http.Client
	UserAgent string
	OAuth     OAuthResponse
	Auth      RedditAuth
	ticker    *time.Ticker
}

type commentListing struct {
	Data struct {
		Children []struct {
			Data Comment
		}
		After string
	}
}

type aboutUser struct {
	Data struct {
		Name      string
		Created   float64 `json:"created_utc"`
		Suspended bool    `json:"is_suspended"`
	}
}

type wikiPage struct {
	Data struct {
		RevisionDate float64
		Content      string `json:"content_md"`
	}
}

func NewScanner(auth RedditAuth, userAgent string) (*Scanner, error) {
	http_client := &http.Client{}
	var client = &Scanner{
		Client:    http_client,
		ticker:    time.NewTicker(time.Second),
		UserAgent: userAgent,
	}

	if err := client.connect(auth); err != nil {
		return nil, err
	}

	return client, nil
}

func (sc *Scanner) UserComments(username string, position string) ([]Comment, string, int, error) {
	return sc.getListing("/u/"+username+"/comments", position)
}

func (sc *Scanner) AboutUser(username string) UserQuery {
	query := UserQuery{User: User{Name: username}}
	sane, err := regexp.MatchString(`^[[:word:]-]+$`, username)
	if err != nil {
		query.Error = err
		return query
	} else if !sane {
		query.Error = fmt.Errorf("username %s contains forbidden characters or is empty", username)
		return query
	}

	sc.Lock()
	defer sc.Unlock()
	res, status, err := sc.rawRequest("GET", "/u/"+username+"/about", nil)
	if err != nil {
		query.Error = err
		return query
	}

	if status == 404 {
		return query
	} else if status != 200 {
		query.Error = fmt.Errorf("bad response status when looking up %s: %d", username, status)
		return query
	}

	about := &aboutUser{}
	if err := json.Unmarshal(res, about); err != nil {
		query.Error = err
		return query
	}

	query.Exists = true
	query.User.Name = about.Data.Name
	query.User.Created = time.Unix(int64(about.Data.Created), 0)
	query.User.Suspended = about.Data.Suspended
	return query
}

func (sc *Scanner) WikiPage(sub, page string) (string, error) {
	sc.Lock()
	defer sc.Unlock()

	path := "/r/" + sub + "/wiki/" + page
	res, status, err := sc.rawRequest("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("invalid status when fetching '%s': %d", path, status)
	}

	parsed := &wikiPage{}
	if err := json.Unmarshal(res, parsed); err != nil {
		return "", err
	}

	return parsed.Data.Content, nil
}

func (sc *Scanner) SubPosts(sub string, position string) ([]Comment, string, error) {
	comments, position, _, err := sc.getListing("/r/"+sub+"/new", position)
	return comments, position, err
}

func (sc *Scanner) connect(auth RedditAuth) error {
	sc.Auth = auth

	var auth_conf = url.Values{
		"grant_type": {"password"},
		"username":   {sc.Auth.Username},
		"password":   {sc.Auth.Password}}
	auth_form := strings.NewReader(auth_conf.Encode())

	req, _ := http.NewRequest("POST", accessTokenURL, auth_form)

	req.Header.Set("User-Agent", sc.UserAgent)
	req.SetBasicAuth(sc.Auth.Id, sc.Auth.Secret)

	res, err := sc.do(req)
	if err != nil {
		return err
	}

	body, read_err := ioutil.ReadAll(res.Body)
	if read_err != nil {
		return read_err
	}

	return json.Unmarshal(body, &sc.OAuth)
}

func (sc *Scanner) getListing(path string, position string) ([]Comment, string, int, error) {
	params := "?sort=new&limit=100"
	if position != "" {
		params += "&after=" + position
	}

	sc.Lock()
	defer sc.Unlock()

	res, status, err := sc.rawRequest("GET", path+params, nil)
	if err != nil {
		return nil, position, 0, err
	}

	if strings.HasPrefix(path, "/u/") && (status == 403 || status == 404) {
		return nil, position, status, nil
	}

	if status != 200 {
		err = fmt.Errorf("bad response status when fetching the listing %s: %d", path, status)
		return nil, position, status, err
	}

	parsed := &commentListing{}
	if err := json.Unmarshal(res, parsed); err != nil {
		return nil, position, status, err
	}

	children := parsed.Data.Children
	comments := make([]Comment, len(children))
	for i, child := range children {
		comments[i] = child.Data.FinishDecoding()
	}

	new_position := parsed.Data.After
	return comments, new_position, status, nil
}

func (sc *Scanner) rawRequest(verb string, path string, data io.Reader) ([]byte, int, error) {

	<-sc.ticker.C

	req, err := http.NewRequest(verb, requestBaseURL+path, data)
	if err != nil {
		return nil, 0, err
	}

	sc.prepareRequest(req)
	res, res_err := sc.Client.Do(req)
	if res_err != nil {
		return nil, 0, res_err
	}

	// The response's body must be read and closed to make sure the underlying TCP connection can be re-used.
	raw_data, read_err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if read_err != nil {
		return nil, 0, read_err
	}

	if res.StatusCode == 401 {
		if err := sc.connect(sc.Auth); err != nil {
			return nil, 0, err
		}
		return sc.rawRequest(verb, path, data)
	}

	return raw_data, res.StatusCode, nil
}

func (sc *Scanner) prepareRequest(req *http.Request) *http.Request {
	req.Header.Set("User-Agent", sc.UserAgent)
	req.Header.Set("Authorization", "bearer "+sc.OAuth.Token)
	return req
}

func (sc *Scanner) do(req *http.Request) (*http.Response, error) {
	return sc.Client.Do(req)
}
