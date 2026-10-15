package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/pkg/protocol"
)

func (s *server) postEvents(w http.ResponseWriter, r *http.Request) {
	var b protocol.EventBatch
	if err := render.DecodeJSON(http.MaxBytesReader(w, r.Body, protocol.MaxBatchBytes), &b); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			render.Render(w, r, errorOf(http.StatusRequestEntityTooLarge, "batch too large"))
			return
		}
		render.Render(w, r, errorOf(http.StatusBadRequest, "invalid batch: "+err.Error()))
		return
	}
	res, err := s.Ingest.IngestEvents(r.Context(), sensorFrom(r.Context()), b)
	if err != nil {
		s.ingestError(w, r, err)
		return
	}
	render.JSON(w, r, res)
}

func (s *server) putTTY(w http.ResponseWriter, r *http.Request) {
	sha, err := protocol.ParseDigest(r.Header.Get(protocol.HeaderDigest))
	if err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, protocol.HeaderDigest+": "+err.Error()))
		return
	}
	if r.ContentLength < 0 {
		render.Render(w, r, errorOf(http.StatusLengthRequired, "Content-Length required"))
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
	render.Status(r, code)
	render.JSON(w, r, res)
}

func (s *server) ingestError(w http.ResponseWriter, r *http.Request, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, ingest.ErrSensorMismatch):
		render.Render(w, r, errorOf(http.StatusForbidden, err.Error()))
	case errors.Is(err, ingest.ErrInvalid):
		render.Render(w, r, errorOf(http.StatusBadRequest, err.Error()))
	case errors.Is(err, ingest.ErrTooLarge), errors.As(err, &tooBig):
		render.Render(w, r, errorOf(http.StatusRequestEntityTooLarge, err.Error()))
	case errors.Is(err, ingest.ErrUnknownSession):
		render.Render(w, r, errorOf(http.StatusNotFound, err.Error()))
	case errors.Is(err, ingest.ErrTTYConflict):
		render.Render(w, r, errorOf(http.StatusConflict, err.Error()))
	case errors.Is(err, ingest.ErrDigestMismatch):
		render.Render(w, r, errorOf(http.StatusUnprocessableEntity, err.Error()))
	default:
		s.internalError(w, r, err)
	}
}
