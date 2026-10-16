package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.env")
	content := "# comment\n\nDB_NAME='arai'\nexport DB_USER=\"arai_user\"\narai_DB_PW=p@ss=word\nARAI_KEEP=fromfile\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARAI_KEEP", "fromenv")
	for _, k := range []string{"DB_NAME", "DB_USER", "arai_DB_PW", "DATABASE_URL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ARAI_KEEP"); got != "fromenv" {
		t.Errorf("existing env var overridden: %q", got)
	}
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://arai_user:p%40ss=word@localhost/arai"
	if c.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", c.DatabaseURL, want)
	}
	if c.Addr != "127.0.0.1:8007" {
		t.Errorf("Addr = %q", c.Addr)
	}
}

func TestLoadEnvFileBadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.env")
	os.WriteFile(path, []byte("NOEQUALS\n"), 0o600)
	if err := LoadEnvFile(path); err == nil {
		t.Error("expected error for line without =")
	}
}
