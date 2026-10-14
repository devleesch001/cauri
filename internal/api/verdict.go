package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/verdict"
)

type verdictReason struct {
	Code   string `json:"code"`
	Points int    `json:"points"`
}

type verdictResponse struct {
	Verdict verdict.Verdict `json:"verdict"`
	Score   int             `json:"score"`
	Reasons []verdictReason `json:"reasons"`
}

// getVerdict tells whether a session looks typed by a human or run by a
// bot, computed on read from its recording and its events.
func (s *server) getVerdict(w http.ResponseWriter, r *http.Request) {
	facts, err := store.GetSessionFacts(r.Context(), s.DB, chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	var typing *verdict.Typing
	if facts.RecordingSHA256 != nil {
		f, err := s.TTY.Open(*facts.RecordingSHA256)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		ty, err := verdict.ReadTyping(f)
		_ = f.Close()
		if err != nil {
			// Judged on the events alone.
			s.Logger.Warn("unreadable recording", "sha256", *facts.RecordingSHA256, "err", err)
		} else {
			typing = &ty
		}
	}

	res := verdict.Judge(verdict.Facts{
		Terminal: facts.Terminal, Commands: facts.Commands, ClientVersion: facts.ClientVersion,
	}, typing)
	out := verdictResponse{Verdict: res.Verdict, Score: res.Score, Reasons: []verdictReason{}}
	for _, reason := range res.Reasons {
		out.Reasons = append(out.Reasons, verdictReason{reason.Code, reason.Points})
	}
	writeJSON(w, http.StatusOK, out)
}
