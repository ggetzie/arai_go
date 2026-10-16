package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// selectLockKey serializes concurrent runs of SelectDaily.
const selectLockKey = 0x61726169 // "arai"

// SelectDaily marks up to DailyCount questions as displayed on today, using
// the algorithm of the Django select_questions command: take the questions
// with the oldest display date among those never tweeted and never voted on
// (1970-01-01 until every question has been shown once, after that the
// oldest recycled day), in their stored random order.
//
// If today already has DailyCount questions nothing changes and alreadyDone
// is true, so a repeated cron run cannot add a second page.
func (s *Store) SelectDaily(ctx context.Context, today time.Time) (selected []Question, alreadyDone bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, selectLockKey); err != nil {
			return err
		}
		var have int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM main_generatedq WHERE displayed = $1`, today).Scan(&have); err != nil {
			return err
		}
		if have >= DailyCount {
			alreadyDone = true
			return nil
		}
		selected, err = collectQuestions(tx.Query(ctx,
			`UPDATE main_generatedq SET displayed = $1
			 WHERE id IN (
			     SELECT id FROM main_generatedq
			     WHERE tweeted = false AND votes = 0 AND displayed = (
			         SELECT min(displayed) FROM main_generatedq WHERE tweeted = false AND votes = 0)
			     ORDER BY randomized, id
			     LIMIT $2
			     FOR UPDATE)
			 RETURNING `+questionCols, today, DailyCount-have))
		return err
	})
	return selected, alreadyDone, err
}
