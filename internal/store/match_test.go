package store_test

import (
	"testing"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// Typed filters: a start by default, * or % anywhere, quotes for exact; a
// complete address is exact; purges (Exact) never go wider than written.
func TestTypedFilters(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		for name, c := range map[string]struct {
			f    store.SessionFilter
			want string // sessions, latest first
		}{
			"start":                {store.SessionFilter{Username: "ad"}, "2"},
			"start, any case":      {store.SessionFilter{Username: "ROO"}, "1"},
			"quoted":               {store.SessionFilter{Username: `"ad"`}, ""},
			"quoted, whole":        {store.SessionFilter{Username: `"admin"`}, "2"},
			"quoted, case":         {store.SessionFilter{Username: `"Root"`}, ""},
			"contains":             {store.SessionFilter{Username: "*dmi*"}, "2"},
			"percent":              {store.SessionFilter{Username: "%dmi%"}, "2"},
			"ends with":            {store.SessionFilter{Password: "%min"}, "21"},
			"everything":           {store.SessionFilter{Password: "*"}, "321"},
			"underscore is itself": {store.SessionFilter{Username: "te_t"}, ""},
			"wildcard inside":      {store.SessionFilter{Username: "te*t"}, "3"},
			"command start":        {store.SessionFilter{Command: "uname"}, "1"},
			"command quoted":       {store.SessionFilter{Command: `"uname"`}, ""},
			"command contains":     {store.SessionFilter{Command: "*cpu*"}, "1"},
			"client":               {store.SessionFilter{Client: "ssh-2.0-lib"}, "3"},
			"complete address":     {store.SessionFilter{SrcIP: "203.0.113.1"}, "1"},
			"incomplete address":   {store.SessionFilter{SrcIP: "203.0.113"}, "321"},
			"purge, no wildcard":   {store.SessionFilter{Username: "ad", Exact: true}, ""},
			"purge, whole":         {store.SessionFilter{Username: "admin", Exact: true}, "2"},
			"purge, wildcard":      {store.SessionFilter{Username: "ad*", Exact: true}, "2"},
			"purge, address start": {store.SessionFilter{SrcIP: "203.0.113", Exact: true}, ""},
		} {
			if _, got := sessions(t, db, c.f); got != c.want {
				t.Errorf("%s: %q, want %q", name, got, c.want)
			}
		}
	})
}
