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

func parse(fs *flag.FlagSet, args []string, fromEnv func(string) bool) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

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
		}
	})
	return errors.Join(errs...)
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
