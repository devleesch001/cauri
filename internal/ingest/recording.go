package ingest

import (
	"os"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttylog"
)

// OpenRecording opens the stored files of a session's recordings as the
// parts of one recording (ttylog.Join), a duplicate moved to its channel's
// start. Files missing from the volume are left out and returned; close
// releases the others.
func (s *Service) OpenRecording(files []store.RecordingFile) (parts []ttylog.Part, missing []string, close func()) {
	var opened []*os.File
	for _, f := range files {
		fd, err := s.tty.Open(f.SHA256)
		if err != nil {
			missing = append(missing, f.SHA256)
			continue
		}
		opened = append(opened, fd)
		p := ttylog.Part{R: fd}
		if f.At != nil {
			p.At = *f.At
		}
		parts = append(parts, p)
	}
	return parts, missing, func() {
		for _, fd := range opened {
			_ = fd.Close()
		}
	}
}
