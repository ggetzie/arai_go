package post

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The worked example from Twitter's "Creating a signature" documentation.
func TestOAuth1SignatureMatchesTwitterExample(t *testing.T) {
	x := &X{
		APIKey:       "xvz1evFS4wEEPTGEFPHBog",
		APISecret:    "kAcSOqF21Fu85e7zjz7ZN2U4ZRhfV3WpwPAoE3Z7kBw",
		AccessToken:  "370773112-GmHxMAgYyLbNEtIKZeRNFsMKPR9EyMZeS9weJAEb",
		AccessSecret: "LswwdoUaIvS8ltyTt5jkRh4J50vUPVVHtR2YPi5kE",
		now:          func() time.Time { return time.Unix(1318622958, 0) },
		nonce:        func() string { return "kYjzVBB8Y0ZFabxSWbWovY3uYSQ2pTgmZeNu2VS4cg" },
	}
	params := url.Values{
		"status":           {"Hello Ladies + Gentlemen, a signed OAuth request!"},
		"include_entities": {"true"},
	}
	got := x.authorization("POST", "https://api.twitter.com/1.1/statuses/update.json", params)
	want := `oauth_signature="hCtSmYh%2BiHYCEqBWrE7C7hYmtUk%3D"`
	if !strings.Contains(got, want) {
		t.Errorf("Authorization = %s\nwant it to contain %s", got, want)
	}
	if !strings.HasPrefix(got, `OAuth oauth_consumer_key="xvz1evFS4wEEPTGEFPHBog", oauth_nonce=`) {
		t.Errorf("unexpected header layout: %s", got)
	}
}

func TestPercentEncode(t *testing.T) {
	for in, want := range map[string]string{
		"Ladies + Gentlemen": "Ladies%20%2B%20Gentlemen",
		"An encoded string!": "An%20encoded%20string%21",
		"Dogs, Cats & Mice":  "Dogs%2C%20Cats%20%26%20Mice",
		"☃":                  "%E2%98%83",
		"a-b.c_d~e":          "a-b.c_d~e",
	} {
		if got := percentEncode(in); got != want {
			t.Errorf("percentEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedditTitle(t *testing.T) {
	for in, want := range map[string]string{
		"What is it?":      "What is it?",
		"Tell me about it": "Tell me about it?",
		"  Padded?\n":      "Padded?",
		"Ends with a dot.": "Ends with a dot.?",
	} {
		if got := RedditTitle(in); got != want {
			t.Errorf("RedditTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func fakeReddit(t *testing.T, submitBody string) (*Reddit, *url.Values) {
	t.Helper()
	var submitted url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		r.ParseForm()
		if id != "cid" || secret != "csecret" || r.Form.Get("grant_type") != "password" ||
			r.Form.Get("username") != "bot" || r.Form.Get("password") != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message": "Unauthorized", "error": 401}`)
			return
		}
		fmt.Fprint(w, `{"access_token": "tok", "token_type": "bearer", "expires_in": 86400, "scope": "*"}`)
	})
	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "bearer tok" || r.Header.Get("User-Agent") != "test-agent" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		r.ParseForm()
		submitted = r.PostForm
		fmt.Fprint(w, submitBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Reddit{
		ClientID: "cid", Secret: "csecret", Username: "bot", Password: "pw",
		UserAgent: "test-agent", Subreddit: "AskRedditButAI",
		TokenURL: srv.URL + "/token", SubmitURL: srv.URL + "/submit",
	}, &submitted
}

func TestRedditSubmit(t *testing.T) {
	r, form := fakeReddit(t, `{"json": {"errors": [], "data": {"url": "https://www.reddit.com/r/AskRedditButAI/comments/ia5zct/x/", "id": "ia5zct", "name": "t3_ia5zct"}}}`)
	id, err := r.Submit(context.Background(), "Why do cats")
	if err != nil || id != "ia5zct" {
		t.Fatalf("Submit = %q, %v", id, err)
	}
	if form.Get("title") != "Why do cats?" || form.Get("sr") != "AskRedditButAI" || form.Get("kind") != "self" || form.Get("api_type") != "json" {
		t.Errorf("submitted form = %v", *form)
	}
}

func TestRedditSubmitErrors(t *testing.T) {
	// Reddit reports rejections with HTTP 200 and an errors list.
	r, _ := fakeReddit(t, `{"json": {"errors": [["RATELIMIT", "you are doing that too much", "ratelimit"]]}}`)
	if _, err := r.Submit(context.Background(), "Q?"); err == nil || !strings.Contains(err.Error(), "RATELIMIT") {
		t.Errorf("errors list: err = %v", err)
	}

	r, _ = fakeReddit(t, `{"json": {"errors": [], "data": {}}}`)
	if _, err := r.Submit(context.Background(), "Q?"); err == nil {
		t.Error("missing id accepted")
	}

	r, _ = fakeReddit(t, `{}`)
	r.Password = "wrong"
	_, err := r.Submit(context.Background(), "Q?")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusUnauthorized {
		t.Errorf("bad password: err = %v", err)
	}
}

func fakeX(t *testing.T, status int, body string) (*X, *string) {
	t.Helper()
	var gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if r.Method != "POST" || !strings.HasPrefix(auth, "OAuth ") || !strings.Contains(auth, `oauth_token="at"`) ||
			!strings.Contains(auth, "oauth_signature=") || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotText = string(b)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return &X{APIKey: "ck", APISecret: "cs", AccessToken: "at", AccessSecret: "as", PostURL: srv.URL}, &gotText
}

func TestXPost(t *testing.T) {
	x, body := fakeX(t, http.StatusCreated, `{"data": {"id": "1846000000000000001", "text": "Why?"}}`)
	id, err := x.Post(context.Background(), " Why? \n")
	if err != nil || id != "1846000000000000001" {
		t.Fatalf("Post = %q, %v", id, err)
	}
	if *body != `{"text":"Why?"}` {
		t.Errorf("request body = %s", *body)
	}
}

func TestXPostErrors(t *testing.T) {
	x, _ := fakeX(t, http.StatusForbidden, `{"detail":"You are not allowed to create a Tweet with duplicate content.","type":"about:blank","title":"Forbidden","status":403}`)
	if _, err := x.Post(context.Background(), "Q?"); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: err = %v", err)
	}
	x, _ = fakeX(t, http.StatusPaymentRequired, `{"title":"CreditsDepleted","status":402}`)
	if _, err := x.Post(context.Background(), "Q?"); err == nil || !strings.Contains(err.Error(), "credits") {
		t.Errorf("402: err = %v", err)
	}
	x, _ = fakeX(t, http.StatusServiceUnavailable, `{"title":"Service Unavailable","status":503}`)
	_, err := x.Post(context.Background(), "Q?")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 503 || errors.Is(err, ErrDuplicate) {
		t.Errorf("503: err = %v", err)
	}
}
