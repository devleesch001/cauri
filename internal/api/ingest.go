package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"cauri/internal/ingest"
	"cauri/pkg/protocol"
)

func (s *server) postEvents(w http.ResponseWriter, r *http.Request) {
	var b protocol.EventBatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, protocol.MaxBatchBytes)).Decode(&b); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "batch too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid batch: "+err.Error())
		return
	}
	res, err := s.Ingest.IngestEvents(r.Context(), sensorFrom(r.Context()), b)
	if err != nil {
		s.ingestError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) putTTY(w http.ResponseWriter, r *http.Request) {
	sha, err := protocol.ParseDigest(r.Header.Get(protocol.HeaderDigest))
	if err != nil {
		writeError(w, http.StatusBadRequest, protocol.HeaderDigest+": "+err.Error())
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "Content-Length required")
		return
	}
	m := protocol.TTYMeta{
		SensorID: r.Header.Get(protocol.HeaderSensor),
		Session:  chi.URLParam(r, "session"),
		SHA256:   sha,
		Size:     r.ContentLength,
	}
	// The service checks the size before reading; the reader bounds what a
	// lying client could still send.
	body := http.MaxBytesReader(w, r.Body, s.Ingest.MaxTTYSize())
	res, err := s.Ingest.StoreTTY(r.Context(), sensorFrom(r.Context()), m, body)
	if err != nil {
		s.ingestError(w, r, err)
		return
	}
	code := http.StatusOK
	if res.Status == protocol.TTYStored {
		code = http.StatusCreated
	}
	writeJSON(w, code, res)
}

func (s *server) ingestError(w http.ResponseWriter, r *http.Request, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, ingest.ErrSensorMismatch):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ingest.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ingest.ErrTooLarge), errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, ingest.ErrUnknownSession):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ingest.ErrTTYConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ingest.ErrDigestMismatch):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.internalError(w, r, err)
	}
}
