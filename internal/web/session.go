package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sidCookie   = "arai_sid"
	adminCookie = "arai_admin"
	adminTTL    = 12 * time.Hour
)

// visitorSession returns the visitor's anonymous session id, issuing a new
// random one in a cookie if the request has none. The id keys arai_vote_state.
func (s *Server) visitorSession(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(sidCookie); err == nil && validSessionID(c.Value) {
		return c.Value
	}
	sid := rand.Text() + rand.Text()
	http.SetCookie(w, &http.Cookie{
		Name:     sidCookie,
		Value:    sid,
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   !s.cfg.Debug,
		SameSite: http.SameSiteLaxMode,
	})
	return sid
}

// existingSession returns the visitor's session id without issuing one.
func existingSession(r *http.Request) string {
	if c, err := r.Cookie(sidCookie); err == nil && validSessionID(c.Value) {
		return c.Value
	}
	return ""
}

func validSessionID(v string) bool {
	if len(v) < 26 || len(v) > 64 {
		return false
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// signAdmin returns a cookie value asserting that user is logged in until expires.
func (s *Server) signAdmin(user string, expires time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(user + "|" + strconv.FormatInt(expires.Unix(), 10)))
	return payload + "." + s.mac(payload)
}

// verifyAdmin checks a cookie value from signAdmin and returns the user.
func (s *Server) verifyAdmin(value string, now time.Time) (string, bool) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(payload))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	user, exp, ok := strings.Cut(string(raw), "|")
	if !ok {
		return "", false
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || !now.Before(time.Unix(unix, 0)) || user != s.cfg.AdminUser {
		return "", false
	}
	return user, true
}

func (s *Server) mac(payload string) string {
	m := hmac.New(sha256.New, []byte(s.cfg.SecretKey))
	m.Write([]byte("arai-admin:" + payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) setAdminCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    value,
		Path:     "/admin",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   !s.cfg.Debug,
		SameSite: http.SameSiteStrictMode,
	})
}
