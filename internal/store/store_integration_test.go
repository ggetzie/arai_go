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

func TestCastVoteSequence(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	id := db.AddQuestion(t, "Why?", storetest.Date(2026, 1, 1), 1, 0, false)

	steps := []struct {
		dir   string
		state int
		votes int
	}{
		{"up", 1, 1},
		{"up", 0, 0},
		{"down", -1, -1},
		{"up", 1, 1},
		{"down", -1, -1},
		{"down", 0, 0},
	}
	for i, s := range steps {
		state, err := db.Store.CastVote(ctx, "sessionA", id, s.dir)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if state != s.state || db.Votes(t, id) != s.votes {
			t.Fatalf("step %d %s: state %d votes %d, want %d %d", i, s.dir, state, db.Votes(t, id), s.state, s.votes)
		}
	}

	// A second visitor's vote is independent.
	if _, err := db.Store.CastVote(ctx, "sessionB", id, "up"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Store.CastVote(ctx, "sessionA", id, "up"); err != nil {
		t.Fatal(err)
	}
	if v := db.Votes(t, id); v != 2 {
		t.Errorf("votes = %d, want 2", v)
	}
	states, err := db.Store.VoteStates(ctx, "sessionA", []int{id, id + 1000})
	if err != nil || states[id] != 1 || len(states) != 1 {
		t.Errorf("VoteStates = %v, %v", states, err)
	}

	if _, err := db.Store.CastVote(ctx, "sessionA", id+1000, "up"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing question: err = %v", err)
	}
	if _, err := db.Store.CastVote(ctx, "sessionA", id, "sideways"); !errors.Is(err, store.ErrBadDirection) {
		t.Errorf("bad direction: err = %v", err)
	}
	if v := db.Votes(t, id); v != 2 {
		t.Errorf("votes changed by rejected votes: %d", v)
	}
}

func TestCastVoteConcurrent(t *testing.T) {
	db := storetest.New(t)
	id := db.AddQuestion(t, "Concurrent?", storetest.Date(2026, 1, 1), 1, 0, false)
	const n = 40
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			if _, err := db.Store.CastVote(context.Background(), fmt.Sprintf("s%d", i), id, "up"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if v := db.Votes(t, id); v != n {
		t.Errorf("votes = %d, want %d (lost updates)", v, n)
	}
}

func TestSelectDaily(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	today := storetest.Date(2026, 10, 16)

	// 30 never-displayed questions in reverse random order, plus questions
	// that must never be picked: voted, tweeted, and already displayed.
	var fresh []int
	for i := range 30 {
		fresh = append(fresh, db.AddQuestion(t, fmt.Sprintf("Fresh %d", i), time.Time{}, 100-i, 0, false))
	}
	voted := db.AddQuestion(t, "Voted", time.Time{}, 0, 3, false)
	tweeted := db.AddQuestion(t, "Tweeted", time.Time{}, 0, 0, true)
	db.AddQuestion(t, "Shown before", storetest.Date(2026, 10, 15), 0, 0, false)

	selected, done, err := db.Store.SelectDaily(ctx, today)
	if err != nil || done {
		t.Fatalf("SelectDaily: done=%v err=%v", done, err)
	}
	if len(selected) != store.DailyCount {
		t.Fatalf("selected %d, want %d", len(selected), store.DailyCount)
	}
	got := map[int]bool{}
	for _, q := range selected {
		got[q.ID] = true
		if !q.Displayed.Equal(today) {
			t.Errorf("question %d displayed %v", q.ID, q.Displayed)
		}
	}
	// Lowest randomized first: the last 25 inserted.
	for i, id := range fresh {
		if want := i >= 5; got[id] != want {
			t.Errorf("fresh %d (id %d): selected=%v, want %v", i, id, got[id], want)
		}
	}
	if got[voted] || got[tweeted] {
		t.Error("selected a voted or tweeted question")
	}

	// A second run the same day changes nothing.
	again, done, err := db.Store.SelectDaily(ctx, today)
	if err != nil || !done || len(again) != 0 {
		t.Fatalf("second run: %d selected, done=%v, err=%v", len(again), done, err)
	}
	if n, _ := db.Store.CountOn(ctx, today); n != store.DailyCount {
		t.Errorf("today has %d questions after rerun", n)
	}

	// Next day: only 5 fresh remain, so just those are taken (min date is still 1970).
	next, _, err := db.Store.SelectDaily(ctx, today.AddDate(0, 0, 1))
	if err != nil || len(next) != 5 {
		t.Fatalf("next day: %d selected, err=%v", len(next), err)
	}

	// Once everything has been shown, the oldest unvoted day is recycled.
	recycled, _, err := db.Store.SelectDaily(ctx, today.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if len(recycled) != 1 || recycled[0].Text != "Shown before" {
		t.Errorf("recycled = %+v", recycled)
	}
}

func TestListingsAndAbout(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	d1, d2 := storetest.Date(2026, 10, 14), storetest.Date(2026, 10, 15)
	db.AddQuestion(t, "B second", d1, 2, 0, false)
	db.AddQuestion(t, "A first", d1, 1, 5, false)
	db.AddQuestion(t, "C other day", d2, 1, 5, true)
	db.AddQuestion(t, "D never", time.Time{}, 1, 1, false)

	qs, err := db.Store.QuestionsOn(ctx, d1, 25, 0)
	if err != nil || len(qs) != 2 || qs[0].Text != "A first" {
		t.Errorf("QuestionsOn = %+v, %v", qs, err)
	}
	dates, err := db.Store.ArchiveDates(ctx, 50, 0)
	if err != nil || len(dates) != 2 || !dates[0].Equal(d2) {
		t.Errorf("ArchiveDates = %v, %v", dates, err)
	}
	if n, _ := db.Store.CountArchiveDates(ctx); n != 2 {
		t.Errorf("CountArchiveDates = %d", n)
	}
	deck, err := db.Store.OnDeck(ctx, 100, 0)
	if err != nil || len(deck) != 2 || deck[0].Text != "A first" {
		t.Errorf("OnDeck = %+v, %v", deck, err)
	}

	intro, err := db.Store.GetOrCreateAbout(ctx, "intro", "hi", "<p>hi</p>")
	if err != nil || intro.HTML != "<p>hi</p>" {
		t.Fatalf("GetOrCreateAbout = %+v, %v", intro, err)
	}
	intro.Text, intro.HTML = "changed", "<p>changed</p>"
	if _, err := db.Store.SaveAbout(ctx, intro); err != nil {
		t.Fatal(err)
	}
	again, _ := db.Store.GetOrCreateAbout(ctx, "intro", "hi", "<p>hi</p>")
	if again.Text != "changed" {
		t.Errorf("GetOrCreateAbout overwrote existing entry: %+v", again)
	}
	if _, err := db.Store.SaveAbout(ctx, store.About{Title: "intro"}); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("duplicate title: err = %v", err)
	}
	if _, err := db.Store.AboutByTitle(ctx, "about"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing about: err = %v", err)
	}
}

func TestAdminQuestionSearchAndUpdate(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	a := db.AddQuestion(t, "What is 100% cats", storetest.Date(2026, 1, 1), 1, 0, true)
	b := db.AddQuestion(t, "What is dogs", time.Time{}, 1, 0, false)

	yes := true
	for _, c := range []struct {
		f    store.QuestionFilter
		want int
	}{
		{store.QuestionFilter{}, 2},
		{store.QuestionFilter{Text: "WHAT"}, 2},
		{store.QuestionFilter{Text: "100%"}, 1},
		{store.QuestionFilter{Text: "%"}, 1},
		{store.QuestionFilter{Tweeted: &yes}, 1},
		{store.QuestionFilter{Displayed: &store.NeverDisplayed}, 1},
	} {
		n, err := db.Store.CountQuestions(ctx, c.f)
		qs, err2 := db.Store.SearchQuestions(ctx, c.f, 50, 0)
		if err != nil || err2 != nil || n != c.want || len(qs) != c.want {
			t.Errorf("filter %+v: count %d, rows %d, want %d (%v %v)", c.f, n, len(qs), c.want, err, err2)
		}
	}

	q, err := db.Store.GetQuestion(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	q.Text, q.Slug, q.Votes = "What is 100% cats", "what-is-100%-cats", 7
	if err := db.Store.UpdateQuestion(ctx, q); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("duplicate slug: err = %v", err)
	}
	q.Text, q.Slug = "What is birds", "what-is-birds"
	if err := db.Store.UpdateQuestion(ctx, q); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Store.GetQuestion(ctx, b)
	if got.Text != "What is birds" || got.Votes != 7 {
		t.Errorf("after update: %+v", got)
	}
	if _, err := db.Store.GetQuestion(ctx, a+b+1000); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing question: err = %v", err)
	}
}

func TestPruneVoteStates(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	id := db.AddQuestion(t, "Prune?", storetest.Date(2026, 1, 1), 1, 0, false)
	db.Store.CastVote(ctx, "old", id, "up")
	db.Store.CastVote(ctx, "new", id, "up")
	db.Conn.Exec(ctx, `UPDATE arai_vote_state SET updated_at = now() - interval '40 days' WHERE session_id = 'old'`)
	n, err := db.Store.PruneVoteStates(ctx, time.Now().AddDate(0, 0, -30))
	if err != nil || n != 1 {
		t.Errorf("pruned %d, err %v", n, err)
	}
	if v := db.Votes(t, id); v != 2 {
		t.Errorf("pruning changed votes: %d", v)
	}
}
