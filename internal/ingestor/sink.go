package ingestor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/devleesch001/cauri/pkg/client"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// ErrPermanent marks a Sink error that will happen again if the same data
// is sent again (conflict, digest mismatch...). Other errors are retried.
var ErrPermanent = errors.New("permanent error")

// Sink receives what the ingestor reads: the API over HTTPS
// (NewHTTPSink), or the ingestion service in-process in the all-in-one
// mode (NewLocalSink).
type Sink interface {
	SendEvents(ctx context.Context, b protocol.EventBatch) (protocol.EventBatchResult, error)
	SendTTY(ctx context.Context, m protocol.TTYMeta, body io.Reader) (protocol.TTYResult, error)
	// SendTTYChannels sends the recordings of a session's channels, which
	// the API merges into the session's one.
	SendTTYChannels(ctx context.Context, sensorID, session string, files []protocol.TTYFile) (protocol.TTYResult, error)
}

// NewHTTPSink returns a Sink posting to the API with the protocol client.
func NewHTTPSink(c *client.Client) Sink { return httpSink{c} }

type httpSink struct{ c *client.Client }

func (s httpSink) SendEvents(ctx context.Context, b protocol.EventBatch) (protocol.EventBatchResult, error) {
	res, err := s.c.SendEvents(ctx, b)
	return res, classify(err)
}

func (s httpSink) SendTTY(ctx context.Context, m protocol.TTYMeta, body io.Reader) (protocol.TTYResult, error) {
	res, err := s.c.SendTTY(ctx, m, body)
	return res, classify(err)
}

func (s httpSink) SendTTYChannels(ctx context.Context, sensorID, session string, files []protocol.TTYFile) (protocol.TTYResult, error) {
	res, err := s.c.SendTTYChannels(ctx, sensorID, session, files)
	return res, classify(err)
}

// classify marks errors tied to the data sent as permanent. Authentication
// errors are not: they come from the configuration, and once it is fixed
// the data must still be delivered.
func classify(err error) error {
	var se *client.StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
			return fmt.Errorf("%w: %w", ErrPermanent, err)
		}
	}
	return err
}
