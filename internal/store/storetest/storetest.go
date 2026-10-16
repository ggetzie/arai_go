// Package storetest sets up an isolated copy of the database schema for
// integration tests. Tests using it are skipped unless ARAI_TEST_DATABASE_URL
// points at a Postgres server where the user may create schemas.
package storetest

import (
	"context"
	"crypto/rand"
	_ "embed"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ggetzie/arai_go/internal/store"
)

//go:embed schema.sql
var djangoSchema string

// DB is a migrated, empty schema plus a direct connection for seeding.
type DB struct {
	Store *store.Store
	Conn  *pgx.Conn
	URL   string
}

// New creates a uniquely named schema with the Django tables and the Go
// migrations applied, and drops it when the test ends.
func New(t testing.TB) *DB {
	t.Helper()
	base := os.Getenv("ARAI_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("ARAI_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schema := "test_" + strings.ToLower(rand.Text()[:12])

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect to schema: %v", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	if _, err := conn.Exec(ctx, strings.ReplaceAll(djangoSchema, "public.", "")); err != nil {
		t.Fatalf("load Django schema: %v", err)
	}

	st, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &DB{Store: st, Conn: conn, URL: u.String()}
}

// AddQuestion inserts a question with Django's column defaults and returns
// its id. displayed may be zero for "never displayed".
func (db *DB) AddQuestion(t testing.TB, text string, displayed time.Time, randomized, votes int, tweeted bool) int {
	t.Helper()
	if displayed.IsZero() {
		displayed = store.NeverDisplayed
	}
	var id int
	err := db.Conn.QueryRow(context.Background(),
		`INSERT INTO main_generatedq (text, slug, displayed, tweeted, tweet_time, votes, randomized, reddit_id, submit_time, submitted)
		 VALUES ($1, $2, $3, $4, '1970-01-01T00:00:00Z', $5, $6, '', '1970-01-01T00:00:00Z', false)
		 RETURNING id`,
		text, strings.ToLower(strings.ReplaceAll(text, " ", "-")), displayed, tweeted, votes, randomized).Scan(&id)
	if err != nil {
		t.Fatalf("add question: %v", err)
	}
	return id
}

// Votes returns a question's vote total.
func (db *DB) Votes(t testing.TB, id int) int {
	t.Helper()
	var v int
	if err := db.Conn.QueryRow(context.Background(), `SELECT votes FROM main_generatedq WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatalf("votes: %v", err)
	}
	return v
}

// Date returns midnight UTC on the given day.
func Date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
