package store

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/cowrie"
)

// restorer puts back into the events of a session whose raw line is gone
// the fields its tables hold (see cowrie.Fragment), so that a session reads
// the same before and after its raw lines are purged. Each login attempt,
// command and transfer row comes from one event of its type, inserted with
// it: rows and events of a type pair up in time order.
type restorer struct {
	s         *Session
	kex       *SSHKex
	logins    map[bool][]LoginAttempt // by success
	commands  []Command
	transfers map[string][]Transfer // by direction
}

func newRestorer(ctx context.Context, db bun.IDB, d *SessionDetail) (*restorer, error) {
	r := &restorer{
		s: &d.Session, commands: d.CommandList,
		logins: map[bool][]LoginAttempt{}, transfers: map[string][]Transfer{},
	}
	for _, l := range d.LoginAttemptList {
		r.logins[l.Success] = append(r.logins[l.Success], l)
	}
	for _, x := range d.Transfers {
		r.transfers[x.Direction] = append(r.transfers[x.Direction], x)
	}
	if d.SSHKexID != nil {
		r.kex = new(SSHKex)
		if err := db.NewSelect().Model(r.kex).Where("id = ?", *d.SSHKexID).Scan(ctx); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// next returns the fields the tables hold of the next event of its type.
// It is called for every event of the session, in order, raw line or not,
// so that the pairing stays aligned.
func (r *restorer) next(eventType string) map[string]any {
	f := map[string]any{}
	set := func(k string, v *string) {
		if v != nil {
			f[k] = *v
		}
	}
	// Cowrie copies the connection on every line of a session.
	set("src_ip", r.s.SrcIP)
	set("dst_ip", r.s.DstIP)
	set("protocol", r.s.Protocol)
	if r.s.SrcPort != nil {
		f["src_port"] = *r.s.SrcPort
	}
	if r.s.DstPort != nil {
		f["dst_port"] = *r.s.DstPort
	}
	switch eventType {
	case cowrie.ClientVersion:
		set("version", r.s.ClientVersion)
	case cowrie.ClientKex:
		if k := r.kex; k != nil {
			set("hassh", k.HASSH)
			for key, v := range map[string]*string{
				"kexAlgs": k.KexAlgs, "encCS": k.EncAlgs, "macCS": k.MacAlgs,
				"compCS": k.CompAlgs, "keyAlgs": k.KeyAlgs, "langCS": k.LangAlgs,
			} {
				if v != nil {
					f[key] = strings.Split(*v, ",")
				}
			}
			if k.KexAlgs != nil && k.EncAlgs != nil && k.MacAlgs != nil && k.CompAlgs != nil {
				f["hasshAlgorithms"] = strings.Join([]string{*k.KexAlgs, *k.EncAlgs, *k.MacAlgs, *k.CompAlgs}, ";")
			}
		}
	case cowrie.LoginSuccess, cowrie.LoginFailed:
		success := eventType == cowrie.LoginSuccess
		if l := r.logins[success]; len(l) > 0 {
			f["username"], f["password"] = l[0].Username, l[0].Password
			r.logins[success] = l[1:]
		}
	case cowrie.CommandInput:
		if len(r.commands) > 0 {
			f["input"] = r.commands[0].Input
			r.commands = r.commands[1:]
		}
	case cowrie.FileDownload:
		if x := r.transfers[Download]; len(x) > 0 {
			set("url", x[0].URL)
			set("destfile", x[0].Filename)
			set("shasum", x[0].SHA256)
			r.transfers[Download] = x[1:]
		}
	case cowrie.FileUpload:
		if x := r.transfers[Upload]; len(x) > 0 {
			set("filename", x[0].Filename)
			set("shasum", x[0].SHA256)
			r.transfers[Upload] = x[1:]
		}
	case cowrie.SessionClosed:
		if r.s.DurationMS != nil {
			f["duration_ms"] = *r.s.DurationMS
		}
	}
	return f
}

// restore merges the fields of the tables into an event's data: what the
// data holds wins, as it holds what a column could not (lenient parsing).
func restore(data *string, fields map[string]any) (json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if data != nil {
		if err := json.Unmarshal([]byte(*data), &out); err != nil {
			return json.RawMessage(*data), nil //nolint:nilerr // not an object: shown as it is
		}
	}
	for k, v := range fields {
		if _, ok := out[k]; ok {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	if len(out) == 0 {
		return nil, nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
