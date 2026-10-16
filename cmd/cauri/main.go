// Command cauri is the single binary of the project. Each deployment role is
// a subcommand, so one Docker image serves every mode.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/devleesch001/cauri/internal/version"
)

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string) error
}

// commands is filled by the init functions of the files defining each
// subcommand, which keeps optional builds (build tags) self-contained.
var commands []command

func register(c command) { commands = append(commands, c) }

func init() {
	register(command{"version", "print the version", func(context.Context, []string) error {
		fmt.Println("cauri " + version.Get().String())
		return nil
	}})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stderr)
	stop()
	switch {
	case err == nil:
	case errors.Is(err, flag.ErrHelp):
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "cauri:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(stderr)
		return flag.ErrHelp
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.run(ctx, args[1:])
		}
	}
	usage(stderr)
	return fmt.Errorf("unknown command %q", args[0])
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cauri <command> [options]\n\nCommands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w, "\nRun 'cauri <command> --help' for the options of a command.")
}
