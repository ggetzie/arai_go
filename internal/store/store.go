// Package store holds all database access. It reads and writes the Django
// tables main_generatedq and main_about in place, plus the Go-owned
// arai_vote_state table created by Migrate.
package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DailyCount is the number of questions shown each day, and the page size of
// the question list.
const DailyCount = 25

// NeverDisplayed is the Django default for main_generatedq.displayed, meaning
// the question has not been shown yet.
var NeverDisplayed = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate")
)

//go:embed migrate.sql
var migrateSQL string

// Question is a row of main_generatedq.
type Question struct {
	ID         int
	Text       string
	Slug       string
	Displayed  time.Time
	Tweeted    bool
	TweetTime  time.Time
	Votes      int
	Randomized int
	Submitted  bool
	SubmitTime time.Time
	RedditID   string
}

// About is a row of main_about.
type About struct {
	ID        int
	Title     string
	Text      string
	HTML      string
	Published bool
}

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies the Go port's idempotent schema additions.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, migrateSQL)
	return err
}

const questionCols = `id, text, slug, displayed, tweeted, tweet_time, votes, randomized, submitted, submit_time, reddit_id`

func scanQuestion(row pgx.Row) (Question, error) {
	var q Question
	err := row.Scan(&q.ID, &q.Text, &q.Slug, &q.Displayed, &q.Tweeted, &q.TweetTime,
		&q.Votes, &q.Randomized, &q.Submitted, &q.SubmitTime, &q.RedditID)
	return q, err
}

func collectQuestions(rows pgx.Rows, err error) ([]Question, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Question, error) { return scanQuestion(r) })
}

// CountOn returns how many questions were displayed on day.
func (s *Store) CountOn(ctx context.Context, day time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM main_generatedq WHERE displayed = $1`, day).Scan(&n)
	return n, err
}

// QuestionsOn returns a page of the questions displayed on day, in their
// random display order.
func (s *Store) QuestionsOn(ctx context.Context, day time.Time, limit, offset int) ([]Question, error) {
	return collectQuestions(s.pool.Query(ctx,
		`SELECT `+questionCols+` FROM main_generatedq WHERE displayed = $1
		 ORDER BY randomized, id LIMIT $2 OFFSET $3`, day, limit, offset))
}

// CountArchiveDates returns the number of distinct days questions were shown.
func (s *Store) CountArchiveDates(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(DISTINCT displayed) FROM main_generatedq WHERE displayed <> $1`, NeverDisplayed).Scan(&n)
	return n, err
}

// ArchiveDates returns a page of the days questions were shown, newest first.
func (s *Store) ArchiveDates(ctx context.Context, limit, offset int) ([]time.Time, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT displayed FROM main_generatedq WHERE displayed <> $1
		 ORDER BY displayed DESC LIMIT $2 OFFSET $3`, NeverDisplayed, limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[time.Time])
}

const onDeckWhere = `tweeted = false AND votes > 0`

// CountOnDeck returns how many upvoted questions have not been tweeted.
func (s *Store) CountOnDeck(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM main_generatedq WHERE `+onDeckWhere).Scan(&n)
	return n, err
}

// OnDeck returns a page of upvoted, untweeted questions, most votes first.
func (s *Store) OnDeck(ctx context.Context, limit, offset int) ([]Question, error) {
	return collectQuestions(s.pool.Query(ctx,
		`SELECT `+questionCols+` FROM main_generatedq WHERE `+onDeckWhere+`
		 ORDER BY votes DESC, displayed DESC, id LIMIT $1 OFFSET $2`, limit, offset))
}

// GetQuestion returns one question by id.
func (s *Store) GetQuestion(ctx context.Context, id int) (Question, error) {
	q, err := scanQuestion(s.pool.QueryRow(ctx,
		`SELECT `+questionCols+` FROM main_generatedq WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return q, ErrNotFound
	}
	return q, err
}

const aboutCols = `id, title, text, html, published`

func scanAbout(row pgx.Row) (About, error) {
	var a About
	err := row.Scan(&a.ID, &a.Title, &a.Text, &a.HTML, &a.Published)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// AboutByTitle returns the About entry with the given title.
func (s *Store) AboutByTitle(ctx context.Context, title string) (About, error) {
	return scanAbout(s.pool.QueryRow(ctx, `SELECT `+aboutCols+` FROM main_about WHERE title = $1`, title))
}

// AboutByID returns the About entry with the given id.
func (s *Store) AboutByID(ctx context.Context, id int) (About, error) {
	return scanAbout(s.pool.QueryRow(ctx, `SELECT `+aboutCols+` FROM main_about WHERE id = $1`, id))
}

// GetOrCreateAbout returns the About entry with title, creating it with the
// given text and html if it does not exist, like Django's get_or_create.
func (s *Store) GetOrCreateAbout(ctx context.Context, title, text, html string) (About, error) {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO main_about (title, text, html, published) VALUES ($1, $2, $3, false)
		 ON CONFLICT (title) DO NOTHING`, title, text, html)
	if err != nil {
		return About{}, err
	}
	return s.AboutByTitle(ctx, title)
}

// ListAbout returns all About entries ordered by title.
func (s *Store) ListAbout(ctx context.Context) ([]About, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+aboutCols+` FROM main_about ORDER BY title`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (About, error) { return scanAbout(r) })
}

// SaveAbout inserts a (when a.ID is 0) or updates it, returning its id.
// ErrDuplicate is returned when the title is already taken.
func (s *Store) SaveAbout(ctx context.Context, a About) (int, error) {
	var err error
	if a.ID == 0 {
		err = s.pool.QueryRow(ctx,
			`INSERT INTO main_about (title, text, html, published) VALUES ($1, $2, $3, $4) RETURNING id`,
			a.Title, a.Text, a.HTML, a.Published).Scan(&a.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = s.pool.Exec(ctx,
			`UPDATE main_about SET title = $2, text = $3, html = $4, published = $5 WHERE id = $1`,
			a.ID, a.Title, a.Text, a.HTML, a.Published)
		if err == nil && tag.RowsAffected() == 0 {
			err = ErrNotFound
		}
	}
	return a.ID, mapUniqueViolation(err)
}

func mapUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicate
	}
	return err
}
