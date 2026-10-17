package api

import (
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"

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
			s.refuse(w, r, http.StatusRequestEntityTooLarge, "batch too large")
			return
		}
		s.refuse(w, r, http.StatusBadRequest, "invalid batch: "+err.Error())
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
	session := chi.URLParam(r, "session")
	sha, err := protocol.ParseDigest(r.Header.Get(protocol.HeaderDigest))
	if err != nil {
		s.refuse(w, r, http.StatusBadRequest, protocol.HeaderDigest+": "+err.Error(), "session", session)
		return
	}
	if r.ContentLength < 0 {
		s.refuse(w, r, http.StatusLengthRequired, "Content-Length required", "session", session)
		return
	}
	m := protocol.TTYMeta{
		SensorID: r.Header.Get(protocol.HeaderSensor),
		Session:  session,
		SHA256:   sha,
		Size:     r.ContentLength,
	}
	// The service checks the size before reading; the reader bounds what a
	// lying client could still send.
	body := http.MaxBytesReader(w, r.Body, s.Ingest.MaxTTYSize())
	res, err := s.Ingest.StoreTTY(r.Context(), sensorFrom(r.Context()), m, body)
	if err != nil {
		s.ingestError(w, r, err, "session", m.Session, "sha256", m.SHA256)
		return
	}
	code := http.StatusOK
	if res.Status == protocol.TTYStored {
		code = http.StatusCreated
	}
	render.Status(r, code)
	render.JSON(w, r, res)
}

// postTTYChannels receives the recordings of a session's channels, a
// multipart/mixed body of one part per file, merged by the service.
func (s *server) postTTYChannels(w http.ResponseWriter, r *http.Request) {
	session := chi.URLParam(r, "session")
	if claimed := r.Header.Get(protocol.HeaderSensor); claimed != sensorFrom(r.Context()) {
		s.ingestError(w, r, ingest.ErrSensorMismatch, "session", session)
		return
	}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" || params["boundary"] == "" {
		s.refuse(w, r, http.StatusUnsupportedMediaType, "want multipart/mixed", "session", session)
		return
	}
	// The service bounds the files; the reader bounds what a lying client
	// could still send, part headers included.
	body := http.MaxBytesReader(w, r.Body, s.Ingest.MaxTTYSize()+protocol.MaxTTYChannels*partOverhead)
	res, err := s.Ingest.StoreTTYChannels(r.Context(), sensorFrom(r.Context()), session, parts{multipart.NewReader(body, params["boundary"])})
	if err != nil {
		s.ingestError(w, r, err, "session", session)
		return
	}
	code := http.StatusOK
	if res.Status == protocol.TTYStored {
		code = http.StatusCreated
	}
	render.Status(r, code)
	render.JSON(w, r, res)
}

// partOverhead bounds what a part adds to its file: boundary and headers.
const partOverhead = 4 << 10

// parts yields the files of a multipart body, described by the headers of
// their part.
type parts struct{ r *multipart.Reader }

func (p parts) Next() (protocol.TTYFile, error) {
	part, err := p.r.NextRawPart()
	if err != nil {
		return protocol.TTYFile{}, err
	}
	sha, err := protocol.ParseDigest(part.Header.Get(protocol.HeaderDigest))
	if err != nil {
		return protocol.TTYFile{}, fmt.Errorf("part %s: %w", protocol.HeaderDigest, err)
	}
	size, err := strconv.ParseInt(part.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return protocol.TTYFile{}, errors.New("part Content-Length required")
	}
	return protocol.TTYFile{SHA256: sha, Size: size, Body: part}, nil
}

// ingestError answers an error of the ingestion service; attrs identify
// what was refused, for the log.
func (s *server) ingestError(w http.ResponseWriter, r *http.Request, err error, attrs ...any) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, ingest.ErrSensorMismatch):
		s.refuse(w, r, http.StatusForbidden, err.Error(), attrs...)
	case errors.Is(err, ingest.ErrInvalid):
		s.refuse(w, r, http.StatusBadRequest, err.Error(), attrs...)
	case errors.Is(err, ingest.ErrTooLarge), errors.As(err, &tooBig):
		s.refuse(w, r, http.StatusRequestEntityTooLarge, err.Error(), attrs...)
	case errors.Is(err, ingest.ErrUnknownSession):
		s.refuse(w, r, http.StatusNotFound, err.Error(), attrs...)
	case errors.Is(err, ingest.ErrTTYConflict):
		s.refuse(w, r, http.StatusConflict, err.Error(), attrs...)
	case errors.Is(err, ingest.ErrDigestMismatch):
		s.refuse(w, r, http.StatusUnprocessableEntity, err.Error(), attrs...)
	default:
		s.internalError(w, r, err)
	}
}

// refuse answers an ingestor's request with an error and logs why, with
// the sensor and attrs: the request log has only the status, and the
// remote address is often a proxy's.
func (s *server) refuse(w http.ResponseWriter, r *http.Request, status int, reason string, attrs ...any) {
	s.Logger.Warn("ingestion refused", append([]any{
		"sensor", sensorFrom(r.Context()), "path", r.URL.Path, "status", status, "reason", reason,
	}, attrs...)...)
	render.Render(w, r, errorOf(status, reason))
}
