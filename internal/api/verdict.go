package api

import (
	"errors"
	"net/http"

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
// bot, computed on read: the stored verdict, used to filter the list, may
// date from older criteria until the API recomputes it.
func (s *server) getVerdict(w http.ResponseWriter, r *http.Request) {
	res, err := s.Ingest.JudgeSession(r.Context(), sessionID(r))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := verdictResponse{Verdict: res.Verdict, Score: res.Score, Reasons: []verdictReason{}}
	for _, reason := range res.Reasons {
		out.Reasons = append(out.Reasons, verdictReason{reason.Code, reason.Points})
	}
	writeJSON(w, http.StatusOK, out)
}
