// Package post publishes questions to Reddit and X.
package post

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Reddit submits self posts to a subreddit with a "script" app's password
// grant. Reddit's public API closes in March 2027; see README.
type Reddit struct {
	ClientID  string
	Secret    string
	Username  string
	Password  string
	UserAgent string
	Subreddit string

	// Overridable for tests.
	TokenURL  string
	SubmitURL string
	HTTP      *http.Client
}

const (
	redditTokenURL  = "https://www.reddit.com/api/v1/access_token"
	redditSubmitURL = "https://oauth.reddit.com/api/submit"
)

// RedditTitle is the post title for a question: Reddit's AskReddit-style
// titles end with a question mark, as the Django version ensured.
func RedditTitle(text string) string {
	t := strings.TrimSpace(text)
	if !strings.HasSuffix(t, "?") {
		t += "?"
	}
	return t
}

// Submit posts the question as a text-less self post and returns the new
// post's id (e.g. "1abc23x").
func (r *Reddit) Submit(ctx context.Context, text string) (string, error) {
	token, err := r.token(ctx)
	if err != nil {
		return "", fmt.Errorf("reddit login: %w", err)
	}
	form := url.Values{
		"api_type":    {"json"},
		"kind":        {"self"},
		"sr":          {r.Subreddit},
		"title":       {RedditTitle(text)},
		"text":        {""},
		"sendreplies": {"false"},
		"resubmit":    {"false"},
		"nsfw":        {"false"},
		"spoiler":     {"false"},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", or(r.SubmitURL, redditSubmitURL), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("User-Agent", r.UserAgent)

	var out struct {
		JSON struct {
			Errors [][]any `json:"errors"`
			Data   struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"data"`
		} `json:"json"`
	}
	if err := doJSON(r.client(), req, http.StatusOK, &out); err != nil {
		return "", fmt.Errorf("reddit submit: %w", err)
	}
	// Reddit reports validation failures and rate limits with HTTP 200 and a
	// non-empty errors list; the Django version treated those as success.
	if len(out.JSON.Errors) > 0 {
		return "", fmt.Errorf("reddit submit rejected: %v", out.JSON.Errors)
	}
	if out.JSON.Data.ID == "" {
		return "", errors.New("reddit submit: response had no post id")
	}
	return out.JSON.Data.ID, nil
}

func (r *Reddit) token(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"username":   {r.Username},
		"password":   {r.Password},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", or(r.TokenURL, redditTokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(r.ClientID, r.Secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", r.UserAgent)

	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := doJSON(r.client(), req, http.StatusOK, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("no access token (error %q)", out.Error)
	}
	return out.AccessToken, nil
}

func (r *Reddit) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return defaultHTTP
}

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

// doJSON sends req and decodes a JSON response with the wanted status into
// out. Other statuses become an error carrying (a bounded amount of) the body.
func doJSON(c *http.Client, req *http.Request, want int, out any) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return &HTTPError{Status: resp.StatusCode, Body: truncate(string(body), 500)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// HTTPError is an unexpected HTTP status from Reddit or X.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
