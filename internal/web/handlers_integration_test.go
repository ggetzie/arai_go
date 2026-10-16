package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ggetzie/arai_go/internal/config"
	"github.com/ggetzie/arai_go/internal/store/storetest"
)

type testSite struct {
	db     *storetest.DB
	srv    *httptest.Server
	client *http.Client
}

var fixedNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func newTestSite(t *testing.T) *testSite {
	t.Helper()
	db := storetest.New(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		SecretKey:         strings.Repeat("k", 32),
		AdminUser:         "admin",
		AdminPasswordHash: string(hash),
		Debug:             true, // plain-HTTP test server, so no Secure cookies
	}
	s, err := New(cfg, db.Store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return fixedNow }
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &testSite{db: db, srv: srv, client: client}
}

func (ts *testSite) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := ts.client.Get(ts.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (ts *testSite) vote(t *testing.T, id any, dir string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"question_id": id, "direction": dir})
	req, _ := http.NewRequest("POST", ts.srv.URL+"/vote/", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestPublicPages(t *testing.T) {
	ts := newTestSite(t)
	today := storetest.Date(2026, 10, 16)
	old := storetest.Date(2020, 10, 1)
	ts.db.AddQuestion(t, "Today's <b>question</b>?", today, 1, 0, false)
	ts.db.AddQuestion(t, "Old question with votes", old, 1, 3, false)
	ts.db.Conn.Exec(t.Context(), `INSERT INTO main_about (title, text, html, published) VALUES ('about', 'x', '<p>About <em>us</em></p>', true)`)

	cases := []struct {
		path   string
		status int
		want   []string
	}{
		{"/", 200, []string{"Today&#39;s &lt;b&gt;question&lt;/b&gt;?", `<div id="intro" class="mb-5"><p>hi</p></div>`, `id="vote-states" type="application/json">{}`}},
		{"/20201001", 200, []string{"Old question with votes"}},
		{"/20201002", 200, []string{"No questions found for this date"}},
		{"/2020100", 404, []string{"Page not found"}},
		{"/20201301", 404, nil},
		{"/?page=2", 404, nil},
		{"/archive/", 200, []string{`href="/20261016">2026-10-16`, `href="/20201001">2020-10-01`}},
		{"/about/", 200, []string{"<p>About <em>us</em></p>"}},
		{"/on-deck/", 200, []string{"1 questions have been upvoted", "Oct 1, 2020", "Votes: 3"}},
		{"/no/such/page", 404, nil},
		{"/static/css/project.min.css", 200, nil},
		{"/static/js/project.js", 200, []string{"loadVoteState"}},
		{"/favicon.ico", 200, nil},
		{"/healthz", 200, []string{"ok"}},
	}
	for _, c := range cases {
		status, body := ts.get(t, c.path)
		if status != c.status {
			t.Errorf("GET %s: status %d, want %d", c.path, status, c.status)
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s: body missing %q", c.path, w)
			}
		}
	}

	resp, err := ts.client.Get(ts.srv.URL + "/archive")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/archive/" {
		t.Errorf("/archive: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestVoting(t *testing.T) {
	ts := newTestSite(t)
	id := ts.db.AddQuestion(t, "Vote on me?", storetest.Date(2026, 10, 16), 1, 0, false)

	// The form posts the id as a string.
	status, out := ts.vote(t, strconv.Itoa(id), "up")
	if status != 200 || out["result"] != "success" || out["vote_state"] != float64(1) || out["question_id"] != float64(id) {
		t.Fatalf("upvote: %d %v", status, out)
	}
	if ts.db.Votes(t, id) != 1 {
		t.Errorf("votes = %d", ts.db.Votes(t, id))
	}

	// The home page restores this visitor's state.
	_, body := ts.get(t, "/")
	if !strings.Contains(body, `{"vote`+strconv.Itoa(id)+`":1}`) {
		t.Errorf("vote state not embedded in home page")
	}

	if status, out = ts.vote(t, id, "down"); status != 200 || out["vote_state"] != float64(-1) || ts.db.Votes(t, id) != -1 {
		t.Errorf("downvote: %d %v votes %d", status, out, ts.db.Votes(t, id))
	}
	if status, out = ts.vote(t, id+1000, "up"); status != 404 || out["error"] == nil {
		t.Errorf("missing question: %d %v", status, out)
	}
	if status, out = ts.vote(t, id, "sideways"); status != 400 || out["error"] == nil {
		t.Errorf("bad direction: %d %v", status, out)
	}

	// Cross-site POSTs are rejected.
	req, _ := http.NewRequest("POST", ts.srv.URL+"/vote/", strings.NewReader(`{"question_id":1,"direction":"up"}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site vote: status %d", resp.StatusCode)
	}
	if status, _ := ts.get(t, "/vote/"); status != http.StatusMethodNotAllowed {
		t.Errorf("GET /vote/: %d", status)
	}
}

func TestAdmin(t *testing.T) {
	ts := newTestSite(t)
	qid := ts.db.AddQuestion(t, "Edit me", storetest.Date(2026, 10, 16), 1, 0, false)
	ts.db.AddQuestion(t, "Taken text", storetest.Date(2026, 10, 16), 2, 0, false)

	// Not logged in: redirected to login with next.
	resp, err := ts.client.Get(ts.srv.URL + "/admin/about/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login?next=%2Fadmin%2Fabout%2F" {
		t.Fatalf("unauthenticated: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		resp, err := ts.client.PostForm(ts.srv.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := post("/admin/login", url.Values{"username": {"admin"}, "password": {"wrong"}}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad password: %d", r.StatusCode)
	}
	r := post("/admin/login", url.Values{"username": {"admin"}, "password": {"correct horse battery"}, "next": {"/admin/about/"}})
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/admin/about/" {
		t.Fatalf("login: %d %q", r.StatusCode, r.Header.Get("Location"))
	}

	// Create the about page from markdown and check it renders publicly.
	r = post("/admin/about/new", url.Values{"title": {"about"}, "text": {"Hello *world*  \nline two"}, "published": {"on"}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("create about: %d", r.StatusCode)
	}
	if _, body := ts.get(t, "/about/"); !strings.Contains(body, "<p>Hello <em>world</em><br />\nline two</p>") {
		t.Errorf("about page not rendered from markdown:\n%s", body)
	}
	if r := post("/admin/about/new", url.Values{"title": {"about"}, "text": {"dup"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("duplicate about title: %d", r.StatusCode)
	}

	// Search and edit a question.
	if status, body := ts.get(t, "/admin/questions/?q=edit"); status != 200 || !strings.Contains(body, "Edit me") || strings.Contains(body, "Taken text") {
		t.Errorf("question search: %d", status)
	}
	form := url.Values{"text": {"Edited, now?"}, "displayed": {"2026-10-16"}, "votes": {"4"}, "reddit_id": {""}}
	if r := post("/admin/questions/"+strconv.Itoa(qid), form); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("save question: %d", r.StatusCode)
	}
	q, _ := ts.db.Store.GetQuestion(t.Context(), qid)
	if q.Text != "Edited, now?" || q.Slug != "edited-now" || q.Votes != 4 {
		t.Errorf("question after save: %+v", q)
	}
	form.Set("text", "Taken text")
	ts.db.Conn.Exec(t.Context(), `UPDATE main_generatedq SET slug = 'taken-text' WHERE text = 'Taken text'`)
	if r := post("/admin/questions/"+strconv.Itoa(qid), form); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("duplicate slug: %d", r.StatusCode)
	}

	// Logout clears access.
	post("/admin/logout", nil)
	resp, _ = ts.client.Get(ts.srv.URL + "/admin/questions/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout: %d", resp.StatusCode)
	}
}
