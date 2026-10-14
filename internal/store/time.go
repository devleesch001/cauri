package store

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Time is a time stored as microseconds since 1970-01-01 UTC, a BIGINT
// in both engines: bun writes time.Time as text under SQLite, where order
// would then depend on every writer using one format. Integers also make
// grouping by hour or day a portable division. In JSON it is RFC 3339.
type Time struct{ time.Time }

// NewTime returns t as stored: in UTC, to the microsecond.
func NewTime(t time.Time) Time { return Time{t.UTC().Truncate(time.Microsecond)} }

// Value implements driver.Valuer.
func (t Time) Value() (driver.Value, error) { return t.UnixMicro(), nil }

// Scan implements sql.Scanner.
func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		t.Time = time.UnixMicro(v).UTC()
		return nil
	case nil:
		t.Time = time.Time{}
		return nil
	default:
		return fmt.Errorf("store.Time: cannot scan %T", src)
	}
}

// Micro returns the stored value of t, for queries.
func Micro(t time.Time) int64 { return t.UnixMicro() }
