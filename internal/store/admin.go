package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// QuestionFilter narrows the admin question search. Zero values mean "any".
type QuestionFilter struct {
	Text      string
	Tweeted   *bool
	Submitted *bool
	Displayed *time.Time
}

func (f QuestionFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t)
		add("text ILIKE $%d", "%"+escaped+"%")
	}
	if f.Tweeted != nil {
		add("tweeted = $%d", *f.Tweeted)
	}
	if f.Submitted != nil {
		add("submitted = $%d", *f.Submitted)
	}
	if f.Displayed != nil {
		add("displayed = $%d", *f.Displayed)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// CountQuestions returns how many questions match f.
func (s *Store) CountQuestions(ctx context.Context, f QuestionFilter) (int, error) {
	where, args := f.where()
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM main_generatedq`+where, args...).Scan(&n)
	return n, err
}

// SearchQuestions returns a page of questions matching f, most recently
// displayed first.
func (s *Store) SearchQuestions(ctx context.Context, f QuestionFilter, limit, offset int) ([]Question, error) {
	where, args := f.where()
	n := len(args)
	args = append(args, limit, offset)
	return collectQuestions(s.pool.Query(ctx,
		`SELECT `+questionCols+` FROM main_generatedq`+where+
			fmt.Sprintf(` ORDER BY displayed DESC, randomized, id LIMIT $%d OFFSET $%d`, n+1, n+2), args...))
}

// UpdateQuestion saves the admin-editable fields of q. The caller is
// responsible for setting q.Slug from q.Text. ErrDuplicate is returned when
// another question already has the same slug.
func (s *Store) UpdateQuestion(ctx context.Context, q Question) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE main_generatedq SET text = $2, slug = $3, displayed = $4, votes = $5,
		     tweeted = $6, submitted = $7, reddit_id = $8
		 WHERE id = $1`,
		q.ID, q.Text, q.Slug, q.Displayed, q.Votes, q.Tweeted, q.Submitted, q.RedditID)
	if err != nil {
		return mapUniqueViolation(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
