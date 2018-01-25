package main

import (
	"encoding/json"
	"errors"
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

const access_token_url = "https://www.reddit.com/api/v1/access_token"

const request_base_url = "https://oauth.reddit.com"

type OAuthResponse struct {
	Token   string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Type    string `json:"token_type"`
	Timeout int    `json:"expires_in"`
	Scope   string `json:"scope"`
}

type RedditScanner interface {
	AboutUser(username string) UserQuery
	UserComments(username string, position string) ([]Comment, string, bool, error)
	SubPosts(sub string, position string) ([]Comment, string, error)
}

type RedditClient struct {
	sync.Mutex
	Client  *http.Client
	Version string
	OAuth   OAuthResponse
	Auth    RedditAuth
	ticker  *time.Ticker
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

func NewRedditClient(auth RedditAuth) (RedditScanner, error) {
	http_client := &http.Client{}
	var client = &RedditClient{
		Client:  http_client,
		Version: "0.1.0",
		ticker:  time.NewTicker(time.Second),
	}

	err := client.Connect(auth)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (rc *RedditClient) UserAgent() string {
	return rc.Auth.Username + "-Bot/v" + rc.Version
}

func (rc *RedditClient) Connect(auth RedditAuth) error {
	rc.Auth = auth

	var auth_conf = url.Values{
		"grant_type": {"password"},
		"username":   {rc.Auth.Username},
		"password":   {rc.Auth.Password}}
	auth_form := strings.NewReader(auth_conf.Encode())

	req, _ := http.NewRequest("POST", access_token_url, auth_form)

	req.Header.Set("User-Agent", rc.UserAgent())
	req.SetBasicAuth(rc.Auth.Id, rc.Auth.Key)

	res, err := rc.Do(req)
	if err != nil {
		return err
	}

	body, read_err := ioutil.ReadAll(res.Body)
	if read_err != nil {
		return read_err
	}

	err = json.Unmarshal(body, &rc.OAuth)
	return err
}

func (rc *RedditClient) PrepareRequest(req *http.Request) *http.Request {
	req.Header.Set("User-Agent", rc.UserAgent())
	req.Header.Set("Authorization", "bearer "+rc.OAuth.Token)
	return req
}

func (rc *RedditClient) Do(req *http.Request) (*http.Response, error) {
	return rc.Client.Do(req)
}

func (rc *RedditClient) RawRequest(verb string, path string, data io.Reader) ([]byte, int, error) {

	<-rc.ticker.C

	req, err := http.NewRequest(verb, request_base_url+path, data)
	if err != nil {
		return nil, 0, err
	}

	rc.PrepareRequest(req)
	res, res_err := rc.Client.Do(req)
	if res_err != nil {
		return nil, 0, res_err
	}

	if res.StatusCode == 401 {
		err = rc.Connect(rc.Auth)
		if err != nil {
			return nil, 0, err
		}
		return rc.RawRequest(verb, path, data)
	}

	raw_data, read_err := ioutil.ReadAll(res.Body)
	if read_err != nil {
		return nil, 0, read_err
	}

	return raw_data, res.StatusCode, nil
}

func (rc *RedditClient) UserComments(username string, position string) ([]Comment, string, bool, error) {
	return rc.getListing("/u/"+username+"/comments", position)
}

func (rc *RedditClient) SubPosts(sub string, position string) ([]Comment, string, error) {
	comments, position, _, err := rc.getListing("/r/"+sub+"/new", position)
	return comments, position, err
}

func (rc *RedditClient) getListing(path string, position string) ([]Comment, string, bool, error) {
	params := "?sort=new&limit=100"
	if position != "" {
		params += "&after=" + position
	}

	rc.Lock()
	defer rc.Unlock()

	res, status, err := rc.RawRequest("GET", path+params, nil)
	if err != nil {
		return nil, position, false, err
	}

	if strings.HasPrefix(path, "/u/") && status == 403 {
		return nil, position, true, nil
	}

	if status != 200 {
		template := "Bad response status when fetching the listing %s: %d"
		msg := fmt.Sprintf(template, path, status)
		return nil, position, false, errors.New(msg)
	}

	parsed := &commentListing{}
	err = json.Unmarshal(res, parsed)
	if err != nil {
		return nil, position, false, err
	}

	children := parsed.Data.Children
	comments := make([]Comment, len(children))
	for i, child := range children {
		comments[i] = child.Data
	}

	new_position := parsed.Data.After
	return comments, new_position, false, nil
}

func (rc *RedditClient) AboutUser(username string) UserQuery {
	query := UserQuery{User: User{Name: username}}
	sane, err := regexp.MatchString(`^[[:word:]-]+$`, username)
	if err != nil {
		query.Error = err
		return query
	} else if !sane {
		msg := fmt.Sprintf("username %s contains forbidden characters or is empty", username)
		query.Error = errors.New(msg)
		return query
	}

	rc.Lock()
	defer rc.Unlock()
	res, status, err := rc.RawRequest("GET", "/u/"+username+"/about", nil)
	if err != nil {
		query.Error = err
		return query
	}

	if status == 404 {
		return query
	} else if status != 200 {
		template := "Bad response status when looking up %s: %d"
		msg := fmt.Sprintf(template, username, status)
		err = errors.New(msg)
		query.Error = err
		return query
	}

	about := &aboutUser{}
	err = json.Unmarshal(res, about)
	if err != nil {
		query.Error = err
		return query
	}

	query.Exists = true
	query.User.Name = about.Data.Name
	query.User.Created = time.Unix(int64(about.Data.Created), 0)
	query.User.Suspended = about.Data.Suspended
	return query
}
