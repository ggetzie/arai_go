// Package web serves the askredditbut.ai site and its admin.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ggetzie/arai_go/internal/config"
	"github.com/ggetzie/arai_go/internal/store"
)

//go:embed templates static
var assets embed.FS

// contentSecurityPolicy allows the site's own assets plus the Bootstrap/jQuery
// CDNs, Google Fonts and the self-hosted analytics.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://code.jquery.com https://cdnjs.cloudflare.com https://stackpath.bootstrapcdn.com https://sa.askredditbut.ai; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data: https://sa.askredditbut.ai; " +
	"connect-src 'self' https://sa.askredditbut.ai; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

type Server struct {
	cfg    *config.Config
	store  *store.Store
	log    *slog.Logger
	now    func() time.Time
	pages  map[string]*template.Template
	static *staticAssets
	login  *loginLimiter
}

func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*Server, error) {
	s := &Server{
		cfg:   cfg,
		store: st,
		log:   log,
		now:   time.Now,
		pages: map[string]*template.Template{},
		login: newLoginLimiter(),
	}
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	if s.static, err = newStaticAssets(staticFS); err != nil {
		return nil, fmt.Errorf("load static files: %w", err)
	}
	funcs := template.FuncMap{
		"static":    s.static.URL,
		"intcomma":  intcomma,
		"ymd":       func(t time.Time) string { return t.Format("20060102") },
		"isodate":   func(t time.Time) string { return t.Format("2006-01-02") },
		"longdate":  func(t time.Time) string { return t.Format("Jan 2, 2006") },
		"displayed": func(t time.Time) bool { return !t.Equal(store.NeverDisplayed) },
		// safeHTML marks admin-authored markdown output as trusted.
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
	}
	pages, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	adminPages, err := fs.Glob(assets, "templates/admin/*.html")
	if err != nil {
		return nil, err
	}
	for _, p := range append(pages, adminPages...) {
		name := strings.TrimPrefix(p, "templates/")
		if name == "base.html" || name == "pagination.html" {
			continue
		}
		t, err := template.New(name).Funcs(funcs).ParseFS(assets, "templates/base.html", "templates/pagination.html", p)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return s, nil
}

// Handler returns the site's root handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	var h http.Handler = mux
	h = http.NewCrossOriginProtection().Handler(h)
	h = s.securityHeaders(h)
	h = s.recoverPanics(h)
	h = s.logRequests(h)
	return h
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		s.static.serve(w, r, "images/favicons/favicon.ico")
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "User-agent: *\nDisallow: /admin/\n")
	})
	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /{date}", s.onDate)
	mux.HandleFunc("POST /vote/", s.vote)
	mux.HandleFunc("/vote/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("GET /archive/", s.archive)
	mux.HandleFunc("GET /about/", s.about)
	mux.HandleFunc("GET /on-deck/", s.onDeck)

	s.adminRoutes(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) })
}

// common holds fields every page template uses.
type common struct {
	Title      string
	ActivePage string
	Admin      bool
	AdminUser  string
	Flash      string
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	t, ok := s.pages[name]
	if !ok {
		s.serverError(w, r, fmt.Errorf("unknown template %q", name))
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		s.serverError(w, r, fmt.Errorf("render %s: %w", name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "404.html", common{Title: "Page not found"})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	t := s.pages["500.html"]
	var buf bytes.Buffer
	if t == nil || t.ExecuteTemplate(&buf, "base", common{Title: "Server Error"}) != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	buf.WriteTo(w)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}

func (s *Server) securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("X-Frame-Options", "DENY")
		hdr.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if !s.cfg.Debug {
			hdr.Set("Content-Security-Policy", contentSecurityPolicy)
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanics(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.serverError(w, r, fmt.Errorf("panic: %v", v))
			}
		}()
		h.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.status == 0 {
		sr.status = code
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	return sr.ResponseWriter.Write(b)
}

func (sr *statusRecorder) Unwrap() http.ResponseWriter { return sr.ResponseWriter }

func (s *Server) logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration", time.Since(start).Round(time.Microsecond), "ip", clientIP(r))
	})
}

// clientIP returns the client address. Behind the local nginx proxy the
// last X-Forwarded-For entry (the one nginx appended) is used.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}

// today returns the current UTC date at midnight, matching how Django stored
// display dates with TIME_ZONE = "UTC".
func (s *Server) today() time.Time {
	y, m, d := s.now().UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// intcomma formats n with thousands separators, like Django's intcomma.
func intcomma(n int) string {
	str := strconv.Itoa(n)
	neg := strings.HasPrefix(str, "-")
	str = strings.TrimPrefix(str, "-")
	var b strings.Builder
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
