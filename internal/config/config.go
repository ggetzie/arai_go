// Package config loads settings from environment variables, optionally seeded
// from a KEY=VALUE env file.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/ggetzie/arai_go/internal/post"
)

// Config holds runtime settings for all subcommands.
type Config struct {
	DatabaseURL       string
	Addr              string
	SecretKey         string
	AdminUser         string
	AdminPasswordHash string
	Debug             bool
}

// LoadEnvFile reads KEY=VALUE lines from path and sets them in the process
// environment. Blank lines and # comments are skipped, an optional "export "
// prefix is allowed, and matching surrounding quotes are stripped. Variables
// already present in the environment are not overridden.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return sc.Err()
}

// FromEnv builds a Config from the environment. The database URL comes from
// DATABASE_URL or, failing that, from the Django .env keys DB_NAME, DB_USER
// and arai_DB_PW with the database on localhost.
func FromEnv() (*Config, error) {
	c := &Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		Addr:              os.Getenv("ARAI_ADDR"),
		SecretKey:         os.Getenv("ARAI_SECRET_KEY"),
		AdminUser:         os.Getenv("ARAI_ADMIN_USER"),
		AdminPasswordHash: os.Getenv("ARAI_ADMIN_PASSWORD_HASH"),
	}
	switch strings.ToLower(os.Getenv("ARAI_DEBUG")) {
	case "1", "true", "yes", "on":
		c.Debug = true
	}
	if c.Addr == "" {
		c.Addr = "127.0.0.1:8007"
	}
	if c.DatabaseURL == "" {
		name, user := os.Getenv("DB_NAME"), os.Getenv("DB_USER")
		if name == "" || user == "" {
			return nil, errors.New("set DATABASE_URL or DB_NAME and DB_USER")
		}
		u := url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(user, os.Getenv("arai_DB_PW")),
			Host:   "localhost",
			Path:   "/" + name,
		}
		c.DatabaseURL = u.String()
	}
	return c, nil
}

// ValidateServe checks the settings that only the web server needs.
func (c *Config) ValidateServe() error {
	if len(c.SecretKey) < 32 {
		return errors.New("ARAI_SECRET_KEY must be at least 32 characters")
	}
	if c.AdminUser == "" || c.AdminPasswordHash == "" {
		return errors.New("ARAI_ADMIN_USER and ARAI_ADMIN_PASSWORD_HASH must be set (see arai hashpw)")
	}
	return nil
}

// Reddit returns the Reddit poster configured by the Django .env keys
// arai_Reddit_id, arai_Reddit_secret, arai_Reddit_username and
// arai_Reddit_password. ARAI_REDDIT_SUBREDDIT (default AskRedditButAI) and
// ARAI_REDDIT_USER_AGENT are optional.
func Reddit() (*post.Reddit, error) {
	r := &post.Reddit{
		ClientID:  os.Getenv("arai_Reddit_id"),
		Secret:    os.Getenv("arai_Reddit_secret"),
		Username:  os.Getenv("arai_Reddit_username"),
		Password:  os.Getenv("arai_Reddit_password"),
		Subreddit: os.Getenv("ARAI_REDDIT_SUBREDDIT"),
		UserAgent: os.Getenv("ARAI_REDDIT_USER_AGENT"),
	}
	if r.ClientID == "" || r.Secret == "" || r.Username == "" || r.Password == "" {
		return nil, errors.New("set arai_Reddit_id, arai_Reddit_secret, arai_Reddit_username and arai_Reddit_password")
	}
	if r.Subreddit == "" {
		r.Subreddit = "AskRedditButAI"
	}
	if r.UserAgent == "" {
		// Reddit's requested format: <platform>:<app id>:<version> (by /u/<username>)
		r.UserAgent = "linux:askredditbut.ai:v2.0 (by /u/" + r.Username + ")"
	}
	return r, nil
}

// X returns the X poster configured by the Django .env keys
// arai_Twitter_API_key, arai_Twitter_API_secret_key,
// arai_Twitter_access_token and arai_Twitter_access_token_secret.
func X() (*post.X, error) {
	x := &post.X{
		APIKey:       os.Getenv("arai_Twitter_API_key"),
		APISecret:    os.Getenv("arai_Twitter_API_secret_key"),
		AccessToken:  os.Getenv("arai_Twitter_access_token"),
		AccessSecret: os.Getenv("arai_Twitter_access_token_secret"),
	}
	if x.APIKey == "" || x.APISecret == "" || x.AccessToken == "" || x.AccessSecret == "" {
		return nil, errors.New("set arai_Twitter_API_key, arai_Twitter_API_secret_key, arai_Twitter_access_token and arai_Twitter_access_token_secret")
	}
	return x, nil
}
