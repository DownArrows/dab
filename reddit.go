package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"strconv"
	"time"
)


const access_token_url = "https://www.reddit.com/api/v1/access_token"


const request_base_url = "https://oauth.reddit.com"


type OAuthResponse struct {
	Token string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Type string `json:"token_type"`
	Timeout int `json:"expires_in"`
	Scope string `json:"scope"`
}


type RedditAuth struct {
	Username string
	Password string
	Id string
	Key string
}


type RedditClient struct {
	Client *http.Client
	RefreshClient *http.Client
	Version string
	OAuth OAuthResponse
	Auth RedditAuth
	ticker *time.Ticker
}


type userComments struct {
	Data struct {
		Children []struct {
			Data Comment
		}
	}
}

func NewRedditClient(auth RedditAuth) (*RedditClient, error) {
	http_client := &http.Client{}
	refresh_client := &http.Client{}
	var client = &RedditClient{
		Client: http_client,
		RefreshClient: refresh_client,
		Version: "0.1.0",
		ticker: time.NewTicker(time.Second),
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
		"username": {rc.Auth.Username},
		"password": {rc.Auth.Password}}
	auth_form := strings.NewReader(auth_conf.Encode())

	req, _ := http.NewRequest("POST", access_token_url, auth_form)

	req.Header.Set("User-Agent", rc.UserAgent())
	req.SetBasicAuth(rc.Auth.Id, rc.Auth.Key)

	res, err := rc.RefreshClient.Do(req)
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

func (rc *RedditClient) PrepareRequest(req *http.Request) *http.Request{
	req.Header.Set("User-Agent", rc.UserAgent())
	req.Header.Set("Authorization", "bearer " + rc.OAuth.Token)
	return req
}

func (rc *RedditClient) Do(req *http.Request) (*http.Response, error) {
	return rc.Client.Do(req)
}

func (rc *RedditClient) RawRequest(verb string, path string, data io.Reader) ([]byte, error) {

	<-rc.ticker.C

	req, err := http.NewRequest(verb, request_base_url + path, data)
	if err != nil {
		return nil, err
	}

	rc.PrepareRequest(req)
	res, res_err := rc.Client.Do(req)
	if res_err != nil {
		return nil, res_err
	}

	if res.StatusCode == 401 {
		err = rc.Connect(rc.Auth)
		if err != nil {
			return nil, err
		}
		return rc.RawRequest(verb, path, data)
	}

	if res.StatusCode != 200 {
		return nil, errors.New("bad status code: " + strconv.Itoa(res.StatusCode))
	}

	raw_data, read_err := ioutil.ReadAll(res.Body)
	if read_err != nil {
		return nil, read_err
	}

	return raw_data, nil
}

func (rc *RedditClient) FetchComments(username string, after string) ([]Comment, error){
	params := "?limit=100&after=" + after
	res, err := rc.RawRequest("GET", "/u/" + username + params, nil)
	if err != nil {
		return nil, err
	}

	parsed := &userComments{}
	err = json.Unmarshal(res, parsed)
	if err != nil {
		return nil, err
	}

	children := parsed.Data.Children
	comments := make([]Comment, len(children))
	for i, child := range children {
		comments[i] = child.Data
	}

	return comments, nil
}
