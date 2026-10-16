package web

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/ggetzie/arai_go/internal/store"
)

type questionListPage struct {
	common
	Intro      template.HTML
	Questions  []store.Question
	Page       *Page
	VoteStates map[string]int
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.questionList(w, r, s.today())
}

// slashRoutes are paths that only exist with a trailing slash; requests for
// the bare form are redirected, like Django's APPEND_SLASH.
var slashRoutes = map[string]bool{"archive": true, "about": true, "on-deck": true, "admin": true}

func (s *Server) onDate(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("date")
	if slashRoutes[raw] {
		http.Redirect(w, r, "/"+raw+"/", http.StatusMovedPermanently)
		return
	}
	day, ok := parseDateParam(raw)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.questionList(w, r, day)
}

// parseDateParam parses the yyyymmdd path segment of /{date}.
func parseDateParam(raw string) (time.Time, bool) {
	if len(raw) != 8 {
		return time.Time{}, false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return time.Time{}, false
		}
	}
	day, err := time.Parse("20060102", raw)
	return day, err == nil
}

func (s *Server) questionList(w http.ResponseWriter, r *http.Request, day time.Time) {
	ctx := r.Context()
	count, err := s.store.CountOn(ctx, day)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := paginate(r.URL, count, store.DailyCount)
	if err != nil {
		s.notFound(w, r)
		return
	}
	questions, err := s.store.QuestionsOn(ctx, day, page.PerPage, page.Offset())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	intro, err := s.store.GetOrCreateAbout(ctx, "intro", "hi", "<p>hi</p>")
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	sid := s.visitorSession(w, r)
	ids := make([]int, len(questions))
	for i, q := range questions {
		ids[i] = q.ID
	}
	states, err := s.store.VoteStates(ctx, sid, ids)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Same shape as the Django session's vote_states, read by project.js.
	voteStates := make(map[string]int, len(states))
	for id, st := range states {
		voteStates["vote"+strconv.Itoa(id)] = st
	}

	s.render(w, r, http.StatusOK, "question_list.html", questionListPage{
		Intro:      template.HTML(intro.HTML),
		Questions:  questions,
		Page:       page,
		VoteStates: voteStates,
	})
}

type voteRequest struct {
	QuestionID json.RawMessage `json:"question_id"`
	Direction  string          `json:"direction"`
}

// questionID accepts the id as a JSON number or a numeric string; the vote
// form posts the hidden input's string value.
func (v voteRequest) questionID() (int, bool) {
	var n int
	if err := json.Unmarshal(v.QuestionID, &n); err == nil {
		return n, true
	}
	var str string
	if err := json.Unmarshal(v.QuestionID, &str); err == nil {
		if n, err := strconv.Atoi(str); err == nil {
			return n, true
		}
	}
	return 0, false
}

func (s *Server) vote(w http.ResponseWriter, r *http.Request) {
	var req voteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request"})
		return
	}
	qid, ok := req.questionID()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Couldn't find that question"})
		return
	}
	sid := s.visitorSession(w, r)
	state, err := s.store.CastVote(r.Context(), sid, qid, req.Direction)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Couldn't find that question"})
	case errors.Is(err, store.ErrBadDirection):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid vote direction"})
	case err != nil:
		s.log.Error("vote failed", "question_id", qid, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Couldn't record your vote"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"result": "success", "question_id": qid, "vote_state": state})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type archivePage struct {
	common
	Dates []time.Time
	Page  *Page
}

func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count, err := s.store.CountArchiveDates(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := paginate(r.URL, count, 50)
	if err != nil {
		s.notFound(w, r)
		return
	}
	dates, err := s.store.ArchiveDates(ctx, page.PerPage, page.Offset())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "archive.html", archivePage{
		common: common{ActivePage: "archive"},
		Dates:  dates,
		Page:   page,
	})
}

type aboutPage struct {
	common
	HTML template.HTML
}

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.AboutByTitle(r.Context(), "about")
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "about.html", aboutPage{
		common: common{ActivePage: "about"},
		HTML:   template.HTML(a.HTML), // admin-authored markdown output
	})
}

type onDeckPage struct {
	common
	Questions []store.Question
	Page      *Page
}

func (s *Server) onDeck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count, err := s.store.CountOnDeck(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := paginate(r.URL, count, 100)
	if err != nil {
		s.notFound(w, r)
		return
	}
	questions, err := s.store.OnDeck(ctx, page.PerPage, page.Offset())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "on_deck.html", onDeckPage{
		common:    common{ActivePage: "on_deck"},
		Questions: questions,
		Page:      page,
	})
}
