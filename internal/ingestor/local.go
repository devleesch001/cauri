package ingestor

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// NewLocalSink returns a Sink calling the ingestion service in-process: the
// all-in-one mode, where the API reads Cowrie's directories itself, with no
// network and no token. The service checks what it receives as it does
// for the HTTP API.
func NewLocalSink(svc *ingest.Service) Sink { return localSink{svc} }

type localSink struct{ svc *ingest.Service }

func (s localSink) SendEvents(ctx context.Context, b protocol.EventBatch) (protocol.EventBatchResult, error) {
	res, err := s.svc.IngestEvents(ctx, b.SensorID, b)
	return res, classifyLocal(err)
}

func (s localSink) SendTTY(ctx context.Context, m protocol.TTYMeta, body io.Reader) (protocol.TTYResult, error) {
	res, err := s.svc.StoreTTY(ctx, m.SensorID, m, body)
	return res, classifyLocal(err)
}

func (s localSink) SendTTYChannels(ctx context.Context, sensorID, session string, files []protocol.TTYFile) (protocol.TTYResult, error) {
	list := ingest.FileList(files)
	res, err := s.svc.StoreTTYChannels(ctx, sensorID, session, &list)
	return res, classifyLocal(err)
}

// classifyLocal marks as permanent the errors the HTTP API answers with a
// status the HTTP sink treats so (see classify): the same data fails the
// same way. An unknown session is retried: its events may still come.
func classifyLocal(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ingest.ErrInvalid), errors.Is(err, ingest.ErrTTYConflict),
		errors.Is(err, ingest.ErrTooLarge), errors.Is(err, ingest.ErrDigestMismatch),
		errors.Is(err, ingest.ErrSensorMismatch):
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	return err
}
