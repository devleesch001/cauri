package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/render"

	"github.com/devleesch001/cauri/internal/store"
)

// MaxExport bounds the rows of an export: beyond, narrow the filters.
const MaxExport = 100_000

// exportSessions writes the sessions of the list filters as CSV.
func (s *server) exportSessions(w http.ResponseWriter, r *http.Request) {
	q := query{v: r.URL.Query()}
	f := sessionFilter(&q)
	if q.err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, q.err.Error()))
		return
	}
	cw := startCSV(w, "sessions")
	_ = cw.Write([]string{"id", "started_at", "sensor", "session", "src_ip", "src_port", "country", "city", "asn", "as_org",
		"client_version", "hassh", "duration_ms", "login_attempts", "shell", "commands", "verdict", "verdict_score", "recording"})
	err := store.EachSession(r.Context(), s.DB, f, MaxExport, func(rows []store.SessionSummary) error {
		for _, x := range rows {
			_ = cw.Write([]string{
				strconv.FormatInt(x.ID, 10), x.StartedAt.Format(time.RFC3339Nano), x.Sensor, x.CowrieSession,
				str(x.SrcIP), num(x.SrcPort), str(x.GeoCountry), str(x.GeoCity), num(x.ASN), str(x.ASOrg),
				str(x.ClientVersion), str(x.HASSH), num(x.DurationMS), strconv.Itoa(x.LoginAttempts),
				strconv.FormatBool(x.LoginSuccess), strconv.Itoa(x.Commands), str(x.Verdict), num(x.VerdictScore),
				strconv.FormatBool(x.TTYAvailable),
			})
		}
		cw.Flush()
		return cw.Error()
	})
	s.endCSV(w, r, cw, err)
}

// exportLoginAttempts writes the login attempts of the list filters as CSV.
func (s *server) exportLoginAttempts(w http.ResponseWriter, r *http.Request) {
	q := query{v: r.URL.Query()}
	f := loginAttemptFilter(&q)
	if q.err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, q.err.Error()))
		return
	}
	cw := startCSV(w, "login-attempts")
	_ = cw.Write([]string{"id", "ts", "sensor", "session_id", "session", "src_ip", "country", "client_version", "username", "password", "success"})
	err := store.EachLoginAttempt(r.Context(), s.DB, f, MaxExport, func(rows []store.LoginAttemptRow) error {
		for _, x := range rows {
			_ = cw.Write([]string{
				strconv.FormatInt(x.ID, 10), x.TS.Format(time.RFC3339Nano), x.Sensor, strconv.FormatInt(x.SessionID, 10), x.Session,
				str(x.SrcIP), str(x.Country), str(x.Client), x.Username, x.Password, strconv.FormatBool(x.Success),
			})
		}
		cw.Flush()
		return cw.Error()
	})
	s.endCSV(w, r, cw, err)
}

// safeWriter neutralises cells a spreadsheet would run as a formula:
// usernames, passwords and client versions come from attackers.
type safeWriter struct{ *csv.Writer }

func (sw safeWriter) Write(record []string) error {
	for i, v := range record {
		if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
			record[i] = "'" + v
		}
	}
	return sw.Writer.Write(record)
}

func startCSV(w http.ResponseWriter, name string) safeWriter {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="cauri-%s-%s.csv"`, name, time.Now().UTC().Format("20060102-150405")))
	return safeWriter{csv.NewWriter(w)}
}

// endCSV finishes an export. Rows already went out with a 200: an error
// can only be logged, and leaves a truncated file.
func (s *server) endCSV(w http.ResponseWriter, r *http.Request, cw safeWriter, err error) {
	cw.Flush()
	if err == nil {
		err = cw.Error()
	}
	if err != nil && r.Context().Err() == nil {
		s.Logger.Error("export", "path", r.URL.Path, "err", err)
	}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func num[T int | int64](p *T) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(int64(*p), 10)
}
