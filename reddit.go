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

type RedditClient struct {
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

func NewRedditClient(auth RedditAuth, userAgent string) (*RedditClient, error) {
	http_client := &http.Client{}
	var client = &RedditClient{
		Client:    http_client,
		ticker:    time.NewTicker(time.Second),
		UserAgent: userAgent,
	}

	err := client.connect(auth)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (rc *RedditClient) UserComments(username string, position string) ([]Comment, string, int, error) {
	return rc.getListing("/u/"+username+"/comments", position)
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
	res, status, err := rc.rawRequest("GET", "/u/"+username+"/about", nil)
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

func (rc *RedditClient) WikiPage(sub, page string) (string, error) {
	rc.Lock()
	defer rc.Unlock()

	path := "/r/" + sub + "/wiki/" + page
	res, status, err := rc.rawRequest("GET", path, nil)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", errors.New(fmt.Sprintf("invalid status when fetching '%s': %d", path, status))
	}

	parsed := &wikiPage{}
	err = json.Unmarshal(res, parsed)
	if err != nil {
		return "", err
	}

	return parsed.Data.Content, nil
}

func (rc *RedditClient) SubPosts(sub string, position string) ([]Comment, string, error) {
	comments, position, _, err := rc.getListing("/r/"+sub+"/new", position)
	return comments, position, err
}

func (rc *RedditClient) connect(auth RedditAuth) error {
	rc.Auth = auth

	var auth_conf = url.Values{
		"grant_type": {"password"},
		"username":   {rc.Auth.Username},
		"password":   {rc.Auth.Password}}
	auth_form := strings.NewReader(auth_conf.Encode())

	req, _ := http.NewRequest("POST", access_token_url, auth_form)

	req.Header.Set("User-Agent", rc.UserAgent)
	req.SetBasicAuth(rc.Auth.Id, rc.Auth.Key)

	res, err := rc.do(req)
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

func (rc *RedditClient) getListing(path string, position string) ([]Comment, string, int, error) {
	params := "?sort=new&limit=100"
	if position != "" {
		params += "&after=" + position
	}

	rc.Lock()
	defer rc.Unlock()

	res, status, err := rc.rawRequest("GET", path+params, nil)
	if err != nil {
		return nil, position, 0, err
	}

	if strings.HasPrefix(path, "/u/") && (status == 403 || status == 404) {
		return nil, position, status, nil
	}

	if status != 200 {
		template := "Bad response status when fetching the listing %s: %d"
		msg := fmt.Sprintf(template, path, status)
		return nil, position, status, errors.New(msg)
	}

	parsed := &commentListing{}
	err = json.Unmarshal(res, parsed)
	if err != nil {
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

func (rc *RedditClient) rawRequest(verb string, path string, data io.Reader) ([]byte, int, error) {

	<-rc.ticker.C

	req, err := http.NewRequest(verb, request_base_url+path, data)
	if err != nil {
		return nil, 0, err
	}

	rc.prepareRequest(req)
	res, res_err := rc.Client.Do(req)
	if res_err != nil {
		return nil, 0, res_err
	}

	if res.StatusCode == 401 {
		err = rc.connect(rc.Auth)
		if err != nil {
			return nil, 0, err
		}
		return rc.rawRequest(verb, path, data)
	}

	raw_data, read_err := ioutil.ReadAll(res.Body)
	if read_err != nil {
		return nil, 0, read_err
	}

	return raw_data, res.StatusCode, nil
}

func (rc *RedditClient) prepareRequest(req *http.Request) *http.Request {
	req.Header.Set("User-Agent", rc.UserAgent)
	req.Header.Set("Authorization", "bearer "+rc.OAuth.Token)
	return req
}

func (rc *RedditClient) do(req *http.Request) (*http.Response, error) {
	return rc.Client.Do(req)
}
