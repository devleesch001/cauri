package main

import (
	"errors"
	"strconv"
	"strings"
)

// retentionDays is how long the raw Cowrie lines are kept: -1 for ever
// (the default), 0 not at all (they are not even stored), N days. An empty
// value or "null" is the default, so that a variable left empty keeps
// everything rather than nothing.
type retentionDays int

const keepForever retentionDays = -1

func (r *retentionDays) String() string { return strconv.Itoa(int(*r)) }

func (r *retentionDays) Set(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "null") {
		*r = keepForever
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < int(keepForever) {
		return errors.New("want -1 (keep for ever), 0 (keep none) or a number of days")
	}
	*r = retentionDays(n)
	return nil
}
