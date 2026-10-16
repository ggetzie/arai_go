package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ggetzie/arai_go/internal/store"
	"github.com/ggetzie/arai_go/internal/store/storetest"
)

func TestPostNextSelectionAndMarking(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	today := storetest.Date(2026, 10, 16)

	db.AddQuestion(t, "Shown today, voting still open", today, 1, 9, false)
	db.AddQuestion(t, "Never displayed", time.Time{}, 1, 9, false)
	db.AddQuestion(t, "No votes", storetest.Date(2026, 10, 1), 1, 0, false)
	db.AddQuestion(t, "Downvoted", storetest.Date(2026, 10, 1), 1, -2, false)
	newer := db.AddQuestion(t, "One vote, newer", storetest.Date(2026, 10, 10), 1, 1, false)
	older := db.AddQuestion(t, "One vote, older", storetest.Date(2026, 10, 2), 1, 1, false)
	top := db.AddQuestion(t, "Two votes", storetest.Date(2026, 10, 12), 1, 2, false)

	// Most votes first, then the longest-waiting.
	var order []int
	for range 3 {
		q, id, err := db.Store.PostNext(ctx, store.Reddit, today, func(q store.Question) (string, error) {
			return fmt.Sprintf("r%d", q.ID), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if id != fmt.Sprintf("r%d", q.ID) {
			t.Errorf("external id %q", id)
		}
		order = append(order, q.ID)
	}
	if fmt.Sprint(order) != fmt.Sprint([]int{top, older, newer}) {
		t.Errorf("post order %v, want %v", order, []int{top, older, newer})
	}
	got, _ := db.Store.GetQuestion(ctx, top)
	if !got.Submitted || got.RedditID != fmt.Sprintf("r%d", top) || got.SubmitTime.Before(time.Now().Add(-time.Minute)) || got.Tweeted {
		t.Errorf("after reddit post: %+v", got)
	}
	if _, _, err := db.Store.PostNext(ctx, store.Reddit, today, func(store.Question) (string, error) {
		t.Error("publish called with nothing to post")
		return "", nil
	}); !errors.Is(err, store.ErrNothingToPost) {
		t.Errorf("drained queue: err = %v", err)
	}

	// X has its own queue: the Reddit posts above don't affect it.
	next, err := db.Store.NextToPost(ctx, store.X, today)
	if err != nil || next.ID != top {
		t.Errorf("X NextToPost = %d, %v; want %d", next.ID, err, top)
	}
	q, _, err := db.Store.PostNext(ctx, store.X, today, func(store.Question) (string, error) { return "x1", nil })
	if err != nil || q.ID != top {
		t.Fatalf("X PostNext = %d, %v", q.ID, err)
	}
	got, _ = db.Store.GetQuestion(ctx, top)
	if !got.Tweeted || got.RedditID != fmt.Sprintf("r%d", top) {
		t.Errorf("X post changed Reddit fields or didn't mark tweeted: %+v", got)
	}
}

// The Django bug: a failed tweet left the question unmarked, and the same
// question was then posted to Reddit on every run. Here a failing publish
// must leave the question unmarked, and the other platform must not repeat.
func TestPostNextFailureDoesNotMarkOrRepeat(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	today := storetest.Date(2026, 10, 16)
	a := db.AddQuestion(t, "First", storetest.Date(2026, 10, 1), 1, 3, false)
	b := db.AddQuestion(t, "Second", storetest.Date(2026, 10, 1), 2, 1, false)

	failing := func(store.Question) (string, error) { return "", errors.New("503 Service Unavailable") }
	for range 3 {
		q, _, err := db.Store.PostNext(ctx, store.X, today, failing)
		if err == nil || q.ID != a {
			t.Fatalf("failing X post: q=%d err=%v", q.ID, err)
		}
	}
	if got, _ := db.Store.GetQuestion(ctx, a); got.Tweeted {
		t.Error("failed X post marked the question tweeted")
	}

	var posted []int
	for range 3 {
		q, _, err := db.Store.PostNext(ctx, store.Reddit, today, func(q store.Question) (string, error) { return "id", nil })
		if errors.Is(err, store.ErrNothingToPost) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		posted = append(posted, q.ID)
	}
	if fmt.Sprint(posted) != fmt.Sprint([]int{a, b}) {
		t.Errorf("reddit posted %v, want each question once: %v", posted, []int{a, b})
	}
}

func TestPostNextConcurrentRunsPostDifferentQuestions(t *testing.T) {
	db := storetest.New(t)
	today := storetest.Date(2026, 10, 16)
	for i := range 5 {
		db.AddQuestion(t, fmt.Sprintf("Q%d", i), storetest.Date(2026, 10, 1), i, 1, false)
	}
	var mu sync.Mutex
	seen := map[int]int{}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			q, _, err := db.Store.PostNext(context.Background(), store.Reddit, today, func(q store.Question) (string, error) {
				time.Sleep(20 * time.Millisecond) // simulate the HTTP call
				return "id", nil
			})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			seen[q.ID]++
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(seen) != 5 {
		t.Errorf("concurrent runs posted %v; want 5 distinct questions once each", seen)
	}
}
