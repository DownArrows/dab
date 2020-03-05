package main

import (
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

// Reddit-related knowledge.
const (
	MaxRedditListingLength = 100
	RedditAPIRequestWait   = time.Second
)

// MatchValidRedditUsername checks if a string is a valid username on Reddit.
var MatchValidRedditUsername = regexp.MustCompile("^[[:word:]-]+$")

const accessTokenRawURL = "https://www.reddit.com/api/v1/access_token"

var requestBaseURL = &url.URL{
	Scheme: "https",
	Host:   "oauth.reddit.com",
}

type oAuthResponse struct {
	Token   string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Type    string `json:"token_type"`
	Timeout int    `json:"expires_in"`
	Scope   string `json:"scope"`
	Error   string `json:"error"`
}

type wikiPage struct {
	Data struct {
		RevisionDate float64
		ContentMD    string `json:"content_md"`
	}
}

type commentListing struct {
	Data struct {
		Children []struct {
			Data struct {
				ID         string
				Author     string
				Score      int64
				Permalink  string
				Subreddit  string
				CreatedUTC float64 `json:"created_utc"`
				Body       string
			}
		}
		After string
	}
}

type aboutUser struct {
	Data struct {
		Name        string
		CreatedUTC  float64 `json:"created_utc"`
		IsSuspended bool    `json:"is_suspended"`
	}
}

type redditResponse struct {
	Data   []byte
	Status int
	Error  error
}

// RedditAPI provides methods to interact with Reddit.
// Note that all exported methods automatically retry once if they got a 403 response.
type RedditAPI struct {
	sync.Mutex
	auth      RedditAuth
	client    *http.Client
	oAuth     oAuthResponse
	ticker    *time.Ticker
	userAgent string
}

// NewRedditAPI creates a data structure to interact with Reddit.
// userAgent is a template for the user agent that will be used in requests.
// It receives a map with the keys "Version", which is the SemVer version of the application,
// and "OS", which is the name of the type of platform (eg. "linux").
// Before use, run the Connect method.
func NewRedditAPI(ctx Ctx, auth RedditAuth, userAgent *template.Template) (*RedditAPI, error) {
	var ua strings.Builder
	data := map[string]interface{}{
		"Version": Version,
		"OS":      runtime.GOOS,
	}
	if err := userAgent.Execute(&ua, data); err != nil {
		return nil, err
	}

	client := &http.Client{}
	ra := &RedditAPI{
		auth:      auth,
		client:    client,
		ticker:    time.NewTicker(RedditAPIRequestWait),
		userAgent: ua.String(),
	}

	return ra, nil
}

// Connect gets a token from Reddit's API which will be used for all requests.
func (ra *RedditAPI) Connect(ctx Ctx) error {
	authConf := url.Values{
		"grant_type": {"password"},
		"username":   {ra.auth.Username},
		"password":   {ra.auth.Password},
	}
	authForm := strings.NewReader(authConf.Encode())

	// This might be called from RedditAPI.rawRequest,
	// so we can't use it to make our request (deadlock),
	// and we have different needs here anyway.
	req, err := http.NewRequest("POST", accessTokenRawURL, authForm)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", ra.userAgent)
	req.SetBasicAuth(ra.auth.ID, ra.auth.Secret)
	req = req.WithContext(ctx)

	res, err := ra.do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	body, readErr := ioutil.ReadAll(res.Body)
	if readErr != nil {
		return readErr
	}

	ra.Lock()
	defer ra.Unlock()
	if err := json.Unmarshal(body, &ra.oAuth); err != nil {
		return err
	}

	if ra.oAuth.Error != "" {
		return fmt.Errorf("error when logging into Reddit: %q", ra.oAuth.Error)
	}

	return nil
}

// UserComments fetches nb comments for a User, and returns a slice of Comment and an updated User.
func (ra *RedditAPI) UserComments(ctx Ctx, user User, nb uint) ([]Comment, User, error) {
	comments, position, status, err := ra.getListing(ctx, "/u/"+user.Name+"/comments", user.Position, nb)
	if err != nil {
		return nil, user, err
	}
	user.Position = position

	// Fetching the comments of a user that's been suspended can return 403,
	// so the status doesn't really give enough information.
	if status == 403 || status == 404 {
		about := ra.AboutUser(ctx, user.Name)
		if about.Error != nil {
			return nil, user, about.Error
		}
		user.Suspended = about.User.Suspended
		user.NotFound = !about.Exists
	}

	return comments, user, nil
}

// AboutUser returns reddit-only data about the user:
//  - whether it exists
//  - whether it is suspended
//  - its name with the correct capitalization
//  - when it was created
// It returns an error without making a request if username contains characters that are forbidden by Reddit.
func (ra *RedditAPI) AboutUser(ctx Ctx, username string) UserQuery {
	query := UserQuery{User: User{Name: username}}

	if !MatchValidRedditUsername.MatchString(username) {
		query.Error = fmt.Errorf("user name %q is an invalid Reddit user name", username)
		return query
	}

	res := ra.request(ctx, "GET", &url.URL{Path: "/u/" + username + "/about"}, nil)
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
	query.User.Created = time.Unix(int64(about.Data.CreatedUTC), 0)
	query.User.Suspended = about.Data.IsSuspended
	return query
}

// WikiPage returns the content of the page of the wiki of a subreddit.
func (ra *RedditAPI) WikiPage(ctx Ctx, sub, page string) (string, error) {
	relativeURL := &url.URL{Path: "/r/" + sub + "/wiki/" + page}
	res := ra.request(ctx, "GET", relativeURL, nil)
	if res.Error != nil {
		return "", res.Error
	}
	if res.Status != 200 {
		return "", fmt.Errorf("invalid status when fetching '%s': %d", relativeURL, res.Status)
	}

	parsed := &wikiPage{}
	if err := json.Unmarshal(res.Data, parsed); err != nil {
		return "", err
	}

	return parsed.Data.ContentMD, nil
}

func (ra *RedditAPI) getListing(ctx Ctx, path, position string, nb uint) ([]Comment, string, int, error) {
	query := url.Values{}
	query.Set("sort", "new")
	query.Set("limit", fmt.Sprintf("%d", nb))
	if position != "" {
		query.Set("after", position)
	}

	res := ra.request(ctx, "GET", &url.URL{Path: path, RawQuery: query.Encode()}, nil)
	if res.Error != nil {
		return nil, position, res.Status, res.Error
	}

	if (res.Status == 403 || res.Status == 404) && strings.HasPrefix(path, "/u/") {
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

	comments := make([]Comment, 0, len(parsed.Data.Children))
	for _, child := range parsed.Data.Children {
		comment := Comment{
			ID:        child.Data.ID,
			Author:    child.Data.Author,
			Score:     child.Data.Score,
			Permalink: child.Data.Permalink,
			Sub:       child.Data.Subreddit,
			Created:   time.Unix(int64(child.Data.CreatedUTC), 0),
			Body:      child.Data.Body,
		}
		comments = append(comments, comment)
	}

	newPosition := parsed.Data.After
	return comments, newPosition, res.Status, nil
}

// Never pass nil as the URL, it can't deal with it. The data argument can be nil though.
func (ra *RedditAPI) request(ctx Ctx, verb string, relativeURL *url.URL, data io.Reader) redditResponse {
	makeReq := func() (*http.Request, error) {
		query, err := url.ParseQuery(relativeURL.RawQuery)
		if err != nil {
			return nil, err
		}
		query.Set("raw_json", "1")
		relativeURL.RawQuery = query.Encode()
		fullURL := requestBaseURL.ResolveReference(relativeURL)
		return http.NewRequest(verb, fullURL.String(), data)
	}
	return ra.rawRequest(ctx, makeReq)
}

// Why take a closure instead of a request object directly?
// Request objects are single use, and this method will automatically retry if
// we are not authenticated anymore.
func (ra *RedditAPI) rawRequest(ctx Ctx, makeReq func() (*http.Request, error)) redditResponse {
	select {
	case <-ra.ticker.C:
		break
	case <-ctx.Done():
		return redditResponse{Error: ctx.Err()}
	}

	req, err := makeReq()
	if err != nil {
		return redditResponse{Error: err}
	}

	ra.prepareRequest(ctx, req)
	rawRes, err := ra.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return redditResponse{Error: err}
	}

	// The response's body must be read and closed to make sure the underlying TCP connection can be re-used.
	rawData, err := ioutil.ReadAll(rawRes.Body)
	rawRes.Body.Close()

	res := redditResponse{
		Status: rawRes.StatusCode,
		Data:   rawData,
		Error:  err,
	}

	if res.Status == 401 {
		if err := ra.Connect(ctx); err != nil {
			res.Error = err
			return res
		}
		return ra.rawRequest(ctx, makeReq)
	}

	return res
}

func (ra *RedditAPI) prepareRequest(ctx Ctx, req *http.Request) *http.Request {
	req.Header.Set("User-Agent", ra.userAgent)
	req.Header.Set("Authorization", "bearer "+ra.oAuth.Token)
	return req.WithContext(ctx)
}

func (ra *RedditAPI) do(req *http.Request) (*http.Response, error) {
	return ra.client.Do(req)
}
