package store

import (
	"maps"
	"slices"
	"strings"

	"github.com/uptrace/bun"
)

// Sort orders a list by one of its columns, named as the list names them,
// descending unless Asc. An empty By is the list's default column.
type Sort struct {
	By  string
	Asc bool
}

// column is a sortable column. A column that may be NULL puts its NULLs
// last both ways: SQLite and PostgreSQL disagree on where they go.
type column struct {
	expr     string
	nullable bool
}

// sortable lists the columns of a list, by name: only these expressions
// reach the SQL, never a value from a request.
type sortable struct {
	def  string
	cols map[string]column
	// tie breaks ties, unique last: paging stays stable. The sorted
	// column is left out of it: repeated, SQLite would not use its index.
	tie []string
}

func (l sortable) names() []string { return slices.Sorted(maps.Keys(l.cols)) }

// order orders q by s, an unknown column being the default one.
func (l sortable) order(q *bun.SelectQuery, s Sort) *bun.SelectQuery {
	c, ok := l.cols[s.By]
	if !ok {
		c = l.cols[l.def]
	}
	dir := " DESC"
	if s.Asc {
		dir = " ASC"
	}
	if c.nullable {
		q = q.OrderExpr("CASE WHEN " + c.expr + " IS NULL THEN 1 ELSE 0 END")
	}
	q = q.OrderExpr(c.expr + dir)
	for _, t := range l.tie {
		if !strings.HasPrefix(t, c.expr+" ") {
			q = q.OrderExpr(t)
		}
	}
	return q
}

var sessionSort = sortable{
	def: "started",
	cols: map[string]column{
		"started":  {"s.started_at", false},
		"sensor":   {"sn.name", false},
		"source":   {"s.src_ip", true},
		"country":  {"s.geo_country", true},
		"duration": {"s.duration_ms", true},
		"commands": {"(SELECT COUNT(*) FROM commands AS sc WHERE sc.session_id = s.id)", false},
		"verdict":  {"s.verdict_score", true},
		"client":   {"s.client_version", true},
	},
	tie: []string{"s.started_at DESC", "s.id DESC"},
}

var loginAttemptSort = sortable{
	def: "time",
	cols: map[string]column{
		"time":     {"la.ts", false},
		"sensor":   {"sn.name", false},
		"source":   {"s.src_ip", true},
		"username": {"la.username", false},
		"password": {"la.password", false},
		"country":  {"s.geo_country", true},
		"client":   {"s.client_version", true},
	},
	tie: []string{"la.ts DESC", "la.id DESC"},
}

// Aggregates are referred to by their aliases, which PostgreSQL accepts in
// ORDER BY only alone: none may be NULL anyway.
var attackerSort = sortable{
	def: SortSessions,
	cols: map[string]column{
		SortSessions: {"n", false},
		SortLastSeen: {"last_ts", false},
		SortShells:   {"shells", false},
	},
	tie: []string{"n DESC", "last_ts DESC", "s.src_ip"},
}

// SessionSorts, LoginAttemptSorts and AttackerSorts name the columns each
// list sorts by.
func SessionSorts() []string      { return sessionSort.names() }
func LoginAttemptSorts() []string { return loginAttemptSort.names() }
func AttackerSorts() []string     { return attackerSort.names() }
