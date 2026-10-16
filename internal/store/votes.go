package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrBadDirection = errors.New("direction must be up or down")

// NextVote applies the voting rules from the Django cast_vote view. prev is
// the visitor's current state for the question (1 up, -1 down, 0 none).
// Voting the same way again removes the vote, voting the other way flips it.
// It returns the new state and the change to the question's vote total.
func NextVote(prev int, direction string) (state, delta int, err error) {
	switch direction {
	case "up":
		switch prev {
		case 1:
			return 0, -1, nil
		case -1:
			return 1, 2, nil
		default:
			return 1, 1, nil
		}
	case "down":
		switch prev {
		case -1:
			return 0, 1, nil
		case 1:
			return -1, -2, nil
		default:
			return -1, -1, nil
		}
	}
	return prev, 0, ErrBadDirection
}

// CastVote records a vote by session on question and returns the session's
// new vote state. The question row is locked so concurrent votes are not lost.
func (s *Store) CastVote(ctx context.Context, sessionID string, questionID int, direction string) (int, error) {
	var state int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var votes int
		err := tx.QueryRow(ctx, `SELECT votes FROM main_generatedq WHERE id = $1 FOR UPDATE`, questionID).Scan(&votes)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}

		prev := 0
		err = tx.QueryRow(ctx, `SELECT state FROM arai_vote_state WHERE session_id = $1 AND question_id = $2`,
			sessionID, questionID).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var delta int
		state, delta, err = NextVote(prev, direction)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE main_generatedq SET votes = votes + $2 WHERE id = $1`, questionID, delta); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO arai_vote_state (session_id, question_id, state, updated_at) VALUES ($1, $2, $3, now())
			 ON CONFLICT (session_id, question_id) DO UPDATE SET state = EXCLUDED.state, updated_at = now()`,
			sessionID, questionID, state)
		return err
	})
	return state, err
}

// VoteStates returns the session's non-neutral vote states for the given
// questions, keyed by question id.
func (s *Store) VoteStates(ctx context.Context, sessionID string, questionIDs []int) (map[int]int, error) {
	states := make(map[int]int)
	if sessionID == "" || len(questionIDs) == 0 {
		return states, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT question_id, state FROM arai_vote_state
		 WHERE session_id = $1 AND question_id = ANY($2) AND state <> 0`, sessionID, questionIDs)
	if err != nil {
		return nil, err
	}
	var id, state int
	_, err = pgx.ForEachRow(rows, []any{&id, &state}, func() error {
		states[id] = state
		return nil
	})
	return states, err
}

// PruneVoteStates deletes vote states not touched since before cutoff. The
// vote totals on main_generatedq are unaffected.
func (s *Store) PruneVoteStates(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM arai_vote_state WHERE updated_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune vote states: %w", err)
	}
	return tag.RowsAffected(), nil
}
