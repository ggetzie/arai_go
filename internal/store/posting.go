package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Platform is a site questions are posted to. Each platform has its own
// "posted" flag on main_generatedq, so a failure on one never causes a
// repeat post on the other.
type Platform string

const (
	Reddit Platform = "reddit"
	X      Platform = "x"
)

var ErrNothingToPost = errors.New("no upvoted questions are waiting to be posted")

// postColumns maps a platform to its flag and timestamp columns. Reddit also
// records the post id in reddit_id.
var postColumns = map[Platform]struct {
	flag, time string
	lockKey    int64
}{
	Reddit: {"submitted", "submit_time", 0x61726169_01},
	X:      {"tweeted", "tweet_time", 0x61726169_02},
}

// postQueueOrder is the order upvoted questions are posted in: most votes
// first, then the longest-waiting. The On Deck page uses the same order.
const postQueueOrder = `votes DESC, displayed, id`

func candidateQuery(flag string) string {
	return `SELECT ` + questionCols + ` FROM main_generatedq
		WHERE ` + flag + ` = false AND votes > 0 AND displayed > $1 AND displayed < $2
		ORDER BY ` + postQueueOrder + ` LIMIT 1`
}

// NextToPost returns the question PostNext would post to p, without posting.
// Questions shown today are excluded because voting on them is still open.
func (s *Store) NextToPost(ctx context.Context, p Platform, today time.Time) (Question, error) {
	cols, ok := postColumns[p]
	if !ok {
		return Question{}, fmt.Errorf("unknown platform %q", p)
	}
	q, err := scanQuestion(s.pool.QueryRow(ctx, candidateQuery(cols.flag), NeverDisplayed, today))
	if errors.Is(err, pgx.ErrNoRows) {
		return q, ErrNothingToPost
	}
	return q, err
}

// PostNext picks the next question for p and calls publish with it. Only
// when publish succeeds is the question marked as posted (with externalID
// stored as reddit_id for Reddit). A per-platform lock is held for the whole
// call so overlapping runs cannot post the same question twice.
//
// If publish succeeds but recording it fails, the returned error says so
// and includes the external id so the row can be fixed by hand.
func (s *Store) PostNext(ctx context.Context, p Platform, today time.Time, publish func(Question) (externalID string, err error)) (Question, string, error) {
	cols, ok := postColumns[p]
	if !ok {
		return Question{}, "", fmt.Errorf("unknown platform %q", p)
	}
	var q Question
	var externalID string
	published := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, cols.lockKey); err != nil {
			return err
		}
		var err error
		q, err = scanQuestion(tx.QueryRow(ctx, candidateQuery(cols.flag)+` FOR UPDATE`, NeverDisplayed, today))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNothingToPost
		} else if err != nil {
			return err
		}

		externalID, err = publish(q)
		if err != nil {
			return err
		}
		published = true

		set := cols.flag + ` = true, ` + cols.time + ` = now()`
		args := []any{q.ID}
		if p == Reddit {
			set += `, reddit_id = $2`
			args = append(args, externalID)
		}
		_, err = tx.Exec(ctx, `UPDATE main_generatedq SET `+set+` WHERE id = $1`, args...)
		return err
	})
	if err != nil && published {
		err = fmt.Errorf("posted question %d to %s as %q but could not record it; mark it posted in the admin to avoid a repeat: %w", q.ID, p, externalID, err)
	}
	return q, externalID, err
}
