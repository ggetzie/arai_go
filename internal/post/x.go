package post

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// X creates posts with the v2 API using OAuth 1.0a user context (the app's
// consumer key/secret plus the account's access token/secret). X bills each
// post against prepaid pay-per-use credits.
type X struct {
	APIKey       string
	APISecret    string
	AccessToken  string
	AccessSecret string

	// Overridable for tests.
	PostURL string
	HTTP    *http.Client
	now     func() time.Time
	nonce   func() string
}

const xPostURL = "https://api.x.com/2/tweets"

// ErrDuplicate means X refused the post because the account already posted
// the same text, so the question can be considered posted.
var ErrDuplicate = errors.New("x: duplicate post")

// Post publishes text and returns the new post's id.
func (x *X) Post(ctx context.Context, text string) (string, error) {
	body, err := json.Marshal(map[string]string{"text": strings.TrimSpace(text)})
	if err != nil {
		return "", err
	}
	endpoint := or(x.PostURL, xPostURL)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// A JSON body is not part of the OAuth 1.0a signature base string.
	req.Header.Set("Authorization", x.authorization("POST", endpoint, nil))

	client := x.HTTP
	if client == nil {
		client = defaultHTTP
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err = doJSON(client, req, http.StatusCreated, &out)
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.Status == http.StatusForbidden && strings.Contains(httpErr.Body, "duplicate"):
			return "", fmt.Errorf("%w: %v", ErrDuplicate, err)
		case httpErr.Status == http.StatusPaymentRequired:
			return "", fmt.Errorf("x: out of pay-per-use credits (top up in the X Developer Console): %w", err)
		}
	}
	if err != nil {
		return "", fmt.Errorf("x post: %w", err)
	}
	if out.Data.ID == "" {
		return "", errors.New("x post: response had no post id")
	}
	return out.Data.ID, nil
}

// authorization builds an OAuth 1.0a HMAC-SHA1 Authorization header.
// params holds any query or form parameters that are part of the signature.
func (x *X) authorization(method, endpoint string, params url.Values) string {
	now := time.Now
	if x.now != nil {
		now = x.now
	}
	nonce := rand.Text
	if x.nonce != nil {
		nonce = x.nonce
	}
	oauth := map[string]string{
		"oauth_consumer_key":     x.APIKey,
		"oauth_nonce":            nonce(),
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(now().Unix(), 10),
		"oauth_token":            x.AccessToken,
		"oauth_version":          "1.0",
	}

	var pairs []string
	for k, v := range oauth {
		pairs = append(pairs, percentEncode(k)+"="+percentEncode(v))
	}
	for k, vs := range params {
		for _, v := range vs {
			pairs = append(pairs, percentEncode(k)+"="+percentEncode(v))
		}
	}
	sort.Strings(pairs)
	base := method + "&" + percentEncode(endpoint) + "&" + percentEncode(strings.Join(pairs, "&"))
	mac := hmac.New(sha1.New, []byte(percentEncode(x.APISecret)+"&"+percentEncode(x.AccessSecret)))
	mac.Write([]byte(base))
	oauth["oauth_signature"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))

	keys := make([]string, 0, len(oauth))
	for k := range oauth {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = percentEncode(k) + `="` + percentEncode(oauth[k]) + `"`
	}
	return "OAuth " + strings.Join(parts, ", ")
}

// percentEncode is RFC 3986 encoding as OAuth 1.0a requires: everything but
// unreserved characters is %XX-encoded (so space is %20, not +).
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
