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
