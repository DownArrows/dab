package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"text/template"
	"time"
)

const accessTokenURL = "https://www.reddit.com/api/v1/access_token"

const requestBaseURL = "https://oauth.reddit.com"

type OAuthResponse struct {
	Token   string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Type    string `json:"token_type"`
	Timeout int    `json:"expires_in"`
	Scope   string `json:"scope"`
}

type wikiPage struct {
	Data struct {
		RevisionDate float64
		Content      string `json:"content_md"`
	}
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
		Name      string  `json:"name"`
		Created   float64 `json:"created_utc"`
		Suspended bool    `json:"is_suspended"`
		ModHash   string  `json:"modhash"`
	}
}

type redditResponse struct {
	Data   []byte
	Status int
	Error  error
}

type Scanner struct {
	sync.Mutex
	Client    *http.Client
	UserAgent string
	OAuth     OAuthResponse
	Auth      RedditAuth
	ticker    *time.Ticker
}

func NewScanner(ctx context.Context, auth RedditAuth, userAgent *template.Template) (*Scanner, error) {
	var user_agent strings.Builder
	data := map[string]interface{}{
		"Version": Version,
		"OS":      runtime.GOOS,
	}
	if err := userAgent.Execute(&user_agent, data); err != nil {
		return nil, err
	}

	http_client := &http.Client{}
	client := &Scanner{
		Auth:      auth,
		Client:    http_client,
		UserAgent: user_agent.String(),
		ticker:    time.NewTicker(time.Second),
	}

	if err := client.Connect(ctx); err != nil {
		return nil, err
	}

	return client, nil
}

func (sc *Scanner) Connect(ctx context.Context) error {
	auth_conf := url.Values{
		"grant_type": {"password"},
		"username":   {sc.Auth.Username},
		"password":   {sc.Auth.Password},
	}
	auth_form := strings.NewReader(auth_conf.Encode())

	req, _ := http.NewRequest("POST", accessTokenURL, auth_form)

	req.Header.Set("User-Agent", sc.UserAgent)
	req.SetBasicAuth(sc.Auth.Id, sc.Auth.Secret)
	req.WithContext(ctx)

	res, err := sc.do(req)
	if err != nil {
		return err
	}

	body, read_err := ioutil.ReadAll(res.Body)
	if read_err != nil {
		return read_err
	}

	sc.Lock()
	defer sc.Unlock()
	return json.Unmarshal(body, &sc.OAuth)
}

func (sc *Scanner) UserComments(ctx context.Context, user User, nb uint) ([]Comment, User, error) {
	comments, position, status, err := sc.getListing(ctx, "/u/"+user.Name+"/comments", user.Position, nb)
	if err != nil {
		return []Comment{}, user, err
	}
	user.Position = position

	// Fetching the comments of a user that's been suspended can return 403,
	// so the status doesn't really give enough information.
	if status == 403 || status == 404 {
		about := sc.AboutUser(ctx, user.Name)
		if about.Error != nil {
			return []Comment{}, user, about.Error
		}
		user.Suspended = about.User.Suspended
		user.NotFound = !about.Exists
	}

	return comments, user, nil
}

func (sc *Scanner) AboutUser(ctx context.Context, username string) UserQuery {
	query := UserQuery{User: User{Name: username}}
	sane, err := regexp.MatchString(`^[[:word:]-]+$`, username)
	if err != nil {
		query.Error = err
		return query
	} else if !sane {
		query.Error = fmt.Errorf("username %s contains forbidden characters or is empty", username)
		return query
	}

	res := sc.Request(ctx, "GET", "/u/"+username+"/about", nil)
	if res.Error != nil {
		query.Error = res.Error
		return query
	}

	if res.Status == 404 {
		query.User.NotFound = true
		return query
	} else if res.Status != 200 {
		query.Error = fmt.Errorf("bad response status when looking up %s: %d", username, res.Status)
		return query
	}

	about := &aboutUser{}
	if err := json.Unmarshal(res.Data, about); err != nil {
		query.Error = err
		return query
	}

	query.Exists = true
	query.User.Name = about.Data.Name
	query.User.Created = int64(about.Data.Created)
	query.User.Suspended = about.Data.Suspended
	return query
}

func (sc *Scanner) WikiPage(ctx context.Context, sub, page string) (string, error) {
	path := "/r/" + sub + "/wiki/" + page
	res := sc.Request(ctx, "GET", path, nil)
	if res.Error != nil {
		return "", res.Error
	}
	if res.Status != 200 {
		return "", fmt.Errorf("invalid status when fetching '%s': %d", path, res.Status)
	}

	parsed := &wikiPage{}
	if err := json.Unmarshal(res.Data, parsed); err != nil {
		return "", err
	}

	return parsed.Data.Content, nil
}

//func (sc *Scanner) EditWikiPage(sub, page, summary, content string) error {
//
//}

func (sc *Scanner) SubPosts(ctx context.Context, sub string, position string) ([]Comment, string, error) {
	comments, position, _, err := sc.getListing(ctx, "/r/"+sub+"/new", position, MaxRedditListingLength)
	return comments, position, err
}

func (sc *Scanner) getListing(ctx context.Context, path, position string, nb uint) ([]Comment, string, int, error) {
	url := fmt.Sprintf("%s?sort=new&limit=%d", path, nb)
	if position != "" {
		url += ("&after=" + position)
	}

	res := sc.Request(ctx, "GET", url, nil)
	if res.Error != nil {
		return nil, position, res.Status, res.Error
	}

	if strings.HasPrefix(path, "/u/") && (res.Status == 403 || res.Status == 404) {
		return nil, position, res.Status, res.Error
	}

	if res.Status != 200 {
		err := fmt.Errorf("bad response status when fetching the listing %s: %d", path, res.Status)
		return nil, position, res.Status, err
	}

	parsed := &commentListing{}
	if err := json.Unmarshal(res.Data, parsed); err != nil {
		return nil, position, res.Status, err
	}

	children := parsed.Data.Children
	comments := make([]Comment, 0, len(children))
	for _, child := range children {
		comments = append(comments, child.Data.FinishDecoding())
	}

	new_position := parsed.Data.After
	return comments, new_position, res.Status, nil
}

func (sc *Scanner) Request(ctx context.Context, verb, path string, data io.Reader) redditResponse {
	make_req := func() (*http.Request, error) {
		return http.NewRequest(verb, requestBaseURL+path, data)
	}
	return sc.rawRequest(ctx, make_req)
}

func (sc *Scanner) rawRequest(ctx context.Context, makeReq func() (*http.Request, error)) redditResponse {
	select {
	case <-sc.ticker.C:
		break
	case <-ctx.Done():
		return redditResponse{Error: ctx.Err()}
	}

	req, err := makeReq()
	if err != nil {
		return redditResponse{Error: err}
	}

	sc.prepareRequest(ctx, req)
	raw_res, err := sc.Client.Do(req)
	if err != nil {
		return redditResponse{Error: err}
	}

	// The response's body must be read and closed to make sure the underlying TCP connection can be re-used.
	raw_data, err := ioutil.ReadAll(raw_res.Body)
	raw_res.Body.Close()

	res := redditResponse{
		Status: raw_res.StatusCode,
		Data:   raw_data,
		Error:  err,
	}

	if res.Status == 401 {
		if err := sc.Connect(ctx); err != nil {
			res.Error = err
			return res
		}
		return sc.rawRequest(ctx, makeReq)
	}

	return res
}

func (sc *Scanner) prepareRequest(ctx context.Context, req *http.Request) *http.Request {
	req.Header.Set("User-Agent", sc.UserAgent)
	req.Header.Set("Authorization", "bearer "+sc.OAuth.Token)
	return req.WithContext(ctx)
}

func (sc *Scanner) do(req *http.Request) (*http.Response, error) {
	return sc.Client.Do(req)
}
