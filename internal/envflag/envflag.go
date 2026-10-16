// Package envflag lets every command-line flag fall back to an environment
// variable, so the same options work from a shell and from a container.
//
// The variable name is derived from the flag name: "db-dsn" becomes
// "CAURI_DB_DSN". A flag given on the command line always wins.
package envflag

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

// Prefix is prepended to every derived environment variable name.
const Prefix = "CAURI_"

// EnvName returns the environment variable backing the flag name.
func EnvName(flagName string) string {
	return Prefix + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// New returns a flag set whose usage message lists environment variables.
func New(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { Usage(fs) }
	return fs
}

// Parse parses args, then sets every flag absent from args from its
// environment variable, if defined.
func Parse(fs *flag.FlagSet, args []string) error {
	return parse(fs, args, func(string) bool { return true })
}

// ParseOnly is Parse with environment variables for the named flags only,
// for commands whose other flags must never come from a forgotten
// variable, such as what to delete.
func ParseOnly(fs *flag.FlagSet, args []string, names ...string) error {
	return parse(fs, args, func(name string) bool { return slices.Contains(names, name) })
}

// Where the value of each flag came from, by flag set, for Describe.
var (
	sourcesMu sync.Mutex
	sources   = map[*flag.FlagSet]map[string]string{}
)

// Sources of a flag's value.
const (
	FromFlag    = "flag"
	FromEnv     = "env"
	FromDefault = "default"
)

func parse(fs *flag.FlagSet, args []string, fromEnv func(string) bool) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	src := map[string]string{}
	for name := range explicit {
		src[name] = FromFlag
	}
	defer func() {
		sourcesMu.Lock()
		sources[fs] = src
		sourcesMu.Unlock()
	}()

	var errs []error
	fs.VisitAll(func(f *flag.Flag) {
		if explicit[f.Name] || !fromEnv(f.Name) {
			return
		}
		env := EnvName(f.Name)
		v, ok := os.LookupEnv(env)
		if !ok {
			return
		}
		if err := fs.Set(f.Name, v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", env, err))
			return
		}
		src[f.Name] = FromEnv
	})
	return errors.Join(errs...)
}

// Option is a flag of a parsed flag set, as Describe tells it.
type Option struct {
	Name    string `json:"name"`    // such as "db-dsn"
	Env     string `json:"env"`     // such as "CAURI_DB_DSN"
	Default string `json:"default"` // empty when none
	Value   string `json:"value"`
	Source  string `json:"source"` // FromFlag, FromEnv or FromDefault
	Usage   string `json:"usage"`
}

// Describe lists the flags of fs, parsed by Parse or ParseOnly, with where
// each value came from, in the order of their names.
func Describe(fs *flag.FlagSet) []Option {
	sourcesMu.Lock()
	src := sources[fs]
	sourcesMu.Unlock()
	var out []Option
	fs.VisitAll(func(f *flag.Flag) {
		o := Option{Name: f.Name, Env: EnvName(f.Name), Default: f.DefValue, Value: f.Value.String(), Source: src[f.Name], Usage: f.Usage}
		if o.Source == "" {
			o.Source = FromDefault
		}
		out = append(out, o)
	})
	return out
}

// Unknown returns the environment variables named with Prefix that are no
// flag of fs, such as a misspelt one, which would be silently ignored.
func Unknown(fs *flag.FlagSet) []string {
	known := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { known[EnvName(f.Name)] = true })
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, Prefix) && !known[name] {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Usage prints the flags of fs along with their environment variables.
func Usage(fs *flag.FlagSet) {
	out := fs.Output()
	fmt.Fprintf(out, "Usage of %s:\n", fs.Name())
	fs.VisitAll(func(f *flag.Flag) {
		name, usage := flag.UnquoteUsage(f)
		fmt.Fprintf(out, "  --%s", f.Name)
		if name != "" {
			fmt.Fprintf(out, " %s", name)
		}
		fmt.Fprintf(out, "\n    \t%s (env %s", usage, EnvName(f.Name))
		if f.DefValue != "" {
			fmt.Fprintf(out, ", default %q", f.DefValue)
		}
		fmt.Fprintln(out, ")")
	})
}
