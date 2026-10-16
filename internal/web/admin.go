package web

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"
	"golang.org/x/crypto/bcrypt"

	"github.com/ggetzie/arai_go/internal/slug"
	"github.com/ggetzie/arai_go/internal/store"
)

// markdown renders About text like python-markdown did for Django: raw HTML
// passes through (the text is admin-authored) and output is XHTML. For the
// existing About entries the output is byte-identical to Django's.
var markdown = goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe(), html.WithXHTML()))

func renderMarkdown(text string) (string, error) {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(text), &buf); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/login", s.adminLoginForm)
	mux.HandleFunc("POST /admin/login", s.adminLogin)
	mux.HandleFunc("POST /admin/logout", s.adminLogout)

	mux.HandleFunc("GET /admin/{$}", s.requireAdmin(func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Redirect(w, r, "/admin/questions/", http.StatusSeeOther)
	}))
	mux.HandleFunc("GET /admin/about/{$}", s.requireAdmin(s.adminAboutList))
	mux.HandleFunc("GET /admin/about/{id}", s.requireAdmin(s.adminAboutForm))
	mux.HandleFunc("POST /admin/about/{id}", s.requireAdmin(s.adminAboutSave))
	mux.HandleFunc("GET /admin/questions/{$}", s.requireAdmin(s.adminQuestionList))
	mux.HandleFunc("GET /admin/questions/{id}", s.requireAdmin(s.adminQuestionForm))
	mux.HandleFunc("POST /admin/questions/{id}", s.requireAdmin(s.adminQuestionSave))
}

type adminHandler func(w http.ResponseWriter, r *http.Request, user string)

func (s *Server) requireAdmin(h adminHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(adminCookie)
		if err == nil {
			if user, ok := s.verifyAdmin(c.Value, s.now()); ok {
				w.Header().Set("Cache-Control", "no-store")
				h(w, r, user)
				return
			}
		}
		http.Redirect(w, r, "/admin/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	}
}

func adminCommon(user, active string) common {
	return common{Title: "Admin", ActivePage: active, Admin: true, AdminUser: user}
}

// safeNext only allows redirects to admin pages on this site.
func safeNext(next string) string {
	if strings.HasPrefix(next, "/admin/") && !strings.HasPrefix(next, "//") && !strings.Contains(next, `\`) {
		return next
	}
	return "/admin/questions/"
}

// --- login ---

type loginPage struct {
	common
	Next  string
	Error string
}

func (s *Server) adminLoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "admin/login.html", loginPage{
		common: common{Title: "Admin login", Admin: true},
		Next:   safeNext(r.URL.Query().Get("next")),
	})
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	next := safeNext(r.PostFormValue("next"))
	fail := func(status int, msg string) {
		s.render(w, r, status, "admin/login.html", loginPage{
			common: common{Title: "Admin login", Admin: true},
			Next:   next,
			Error:  msg,
		})
	}
	if !s.login.allowed(ip, s.now()) {
		fail(http.StatusTooManyRequests, "Too many failed attempts. Try again later.")
		return
	}
	user := r.PostFormValue("username")
	pass := r.PostFormValue("password")
	// Always run bcrypt so response time doesn't reveal whether the user exists.
	pwErr := bcrypt.CompareHashAndPassword([]byte(s.cfg.AdminPasswordHash), []byte(pass))
	if user != s.cfg.AdminUser || pwErr != nil {
		s.login.fail(ip, s.now())
		s.log.Warn("admin login failed", "ip", ip, "user", user)
		fail(http.StatusUnauthorized, "Incorrect username or password.")
		return
	}
	s.login.reset(ip)
	s.log.Info("admin login", "ip", ip, "user", user)
	s.setAdminCookie(w, s.signAdmin(user, s.now().Add(adminTTL)), int(adminTTL.Seconds()))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	s.setAdminCookie(w, "", -1)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// loginLimiter blocks an IP for a while after repeated failed logins.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempts
}

type loginAttempts struct {
	failures int
	first    time.Time
}

const (
	maxLoginFailures = 5
	loginWindow      = 15 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string]*loginAttempts{}}
}

func (l *loginLimiter) allowed(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.attempts[ip]
	if !ok {
		return true
	}
	if now.Sub(a.first) > loginWindow {
		delete(l.attempts, ip)
		return true
	}
	return a.failures < maxLoginFailures
}

func (l *loginLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, a := range l.attempts {
		if now.Sub(a.first) > loginWindow {
			delete(l.attempts, k)
		}
	}
	a, ok := l.attempts[ip]
	if !ok {
		a = &loginAttempts{first: now}
		l.attempts[ip] = a
	}
	a.failures++
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}

// --- About ---

type aboutListPage struct {
	common
	Entries []store.About
}

func (s *Server) adminAboutList(w http.ResponseWriter, r *http.Request, user string) {
	entries, err := s.store.ListAbout(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	c := adminCommon(user, "admin_about")
	c.Flash = flash(r)
	s.render(w, r, http.StatusOK, "admin/about_list.html", aboutListPage{common: c, Entries: entries})
}

type aboutFormPage struct {
	common
	Entry store.About
	New   bool
	Error string
}

// aboutFromPath loads the About entry for /admin/about/{id}; "new" yields an
// empty entry.
func (s *Server) aboutFromPath(w http.ResponseWriter, r *http.Request) (store.About, bool) {
	raw := r.PathValue("id")
	if raw == "new" {
		return store.About{}, true
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		s.notFound(w, r)
		return store.About{}, false
	}
	a, err := s.store.AboutByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return a, false
	} else if err != nil {
		s.serverError(w, r, err)
		return a, false
	}
	return a, true
}

func (s *Server) adminAboutForm(w http.ResponseWriter, r *http.Request, user string) {
	a, ok := s.aboutFromPath(w, r)
	if !ok {
		return
	}
	c := adminCommon(user, "admin_about")
	c.Flash = flash(r)
	s.render(w, r, http.StatusOK, "admin/about_form.html", aboutFormPage{common: c, Entry: a, New: a.ID == 0})
}

func (s *Server) adminAboutSave(w http.ResponseWriter, r *http.Request, user string) {
	a, ok := s.aboutFromPath(w, r)
	if !ok {
		return
	}
	a.Title = strings.TrimSpace(r.PostFormValue("title"))
	a.Text = strings.ReplaceAll(r.PostFormValue("text"), "\r\n", "\n")
	a.Published = r.PostFormValue("published") == "on"

	formErr := func(msg string) {
		s.render(w, r, http.StatusUnprocessableEntity, "admin/about_form.html", aboutFormPage{
			common: adminCommon(user, "admin_about"), Entry: a, New: a.ID == 0, Error: msg,
		})
	}
	if a.Title == "" || len([]rune(a.Title)) > 30 {
		formErr("Title is required and must be at most 30 characters.")
		return
	}
	htmlOut, err := renderMarkdown(a.Text)
	if err != nil {
		formErr("Couldn't render markdown: " + err.Error())
		return
	}
	a.HTML = htmlOut
	id, err := s.store.SaveAbout(r.Context(), a)
	if errors.Is(err, store.ErrDuplicate) {
		formErr("Another entry already has that title.")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("admin saved about", "user", user, "id", id, "title", a.Title)
	http.Redirect(w, r, "/admin/about/"+strconv.Itoa(id)+"?saved=1", http.StatusSeeOther)
}

// --- Questions ---

type questionListAdminPage struct {
	common
	Questions []store.Question
	Page      *Page
	Query     url.Values
}

func parseTriState(v string) *bool {
	switch v {
	case "yes":
		t := true
		return &t
	case "no":
		f := false
		return &f
	}
	return nil
}

func (s *Server) adminQuestionList(w http.ResponseWriter, r *http.Request, user string) {
	q := r.URL.Query()
	f := store.QuestionFilter{
		Text:      q.Get("q"),
		Tweeted:   parseTriState(q.Get("tweeted")),
		Submitted: parseTriState(q.Get("submitted")),
	}
	if d := q.Get("displayed"); d != "" {
		if day, err := time.Parse("2006-01-02", d); err == nil {
			f.Displayed = &day
		}
	}
	ctx := r.Context()
	count, err := s.store.CountQuestions(ctx, f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := paginate(r.URL, count, 50)
	if err != nil {
		s.notFound(w, r)
		return
	}
	questions, err := s.store.SearchQuestions(ctx, f, page.PerPage, page.Offset())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	c := adminCommon(user, "admin_questions")
	c.Flash = flash(r)
	s.render(w, r, http.StatusOK, "admin/question_list.html", questionListAdminPage{
		common: c, Questions: questions, Page: page, Query: q,
	})
}

type questionFormPage struct {
	common
	Q     store.Question
	Error string
}

func (s *Server) questionFromPath(w http.ResponseWriter, r *http.Request) (store.Question, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r)
		return store.Question{}, false
	}
	q, err := s.store.GetQuestion(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return q, false
	} else if err != nil {
		s.serverError(w, r, err)
		return q, false
	}
	return q, true
}

func (s *Server) adminQuestionForm(w http.ResponseWriter, r *http.Request, user string) {
	q, ok := s.questionFromPath(w, r)
	if !ok {
		return
	}
	c := adminCommon(user, "admin_questions")
	c.Flash = flash(r)
	s.render(w, r, http.StatusOK, "admin/question_form.html", questionFormPage{common: c, Q: q})
}

func (s *Server) adminQuestionSave(w http.ResponseWriter, r *http.Request, user string) {
	q, ok := s.questionFromPath(w, r)
	if !ok {
		return
	}
	formErr := func(msg string) {
		s.render(w, r, http.StatusUnprocessableEntity, "admin/question_form.html", questionFormPage{
			common: adminCommon(user, "admin_questions"), Q: q, Error: msg,
		})
	}
	q.Text = strings.TrimSpace(r.PostFormValue("text"))
	q.RedditID = strings.TrimSpace(r.PostFormValue("reddit_id"))
	q.Tweeted = r.PostFormValue("tweeted") == "on"
	q.Submitted = r.PostFormValue("submitted") == "on"
	if n := len([]rune(q.Text)); n == 0 || n > 280 {
		formErr("Text is required and must be at most 280 characters.")
		return
	}
	if len(q.RedditID) > 30 {
		formErr("Reddit post id must be at most 30 characters.")
		return
	}
	votes, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("votes")))
	if err != nil {
		formErr("Votes must be a whole number.")
		return
	}
	q.Votes = votes
	day, err := time.Parse("2006-01-02", r.PostFormValue("displayed"))
	if err != nil {
		formErr("Displayed must be a date (YYYY-MM-DD).")
		return
	}
	q.Displayed = day
	q.Slug = slug.Make(q.Text)
	if q.Slug == "" {
		formErr("Text must contain at least one letter or number.")
		return
	}
	err = s.store.UpdateQuestion(r.Context(), q)
	if errors.Is(err, store.ErrDuplicate) {
		formErr("Another question already has the same slug (" + q.Slug + ").")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("admin saved question", "user", user, "id", q.ID)
	http.Redirect(w, r, "/admin/questions/"+strconv.Itoa(q.ID)+"?saved=1", http.StatusSeeOther)
}

func flash(r *http.Request) string {
	if r.URL.Query().Get("saved") == "1" {
		return "Saved."
	}
	return ""
}
