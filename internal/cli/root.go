package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"dockvault/internal/version"
)

// command describes a single dockvault subcommand. run receives the
// process-lifetime context set up in Execute (cancelled on SIGINT/SIGTERM
// via main.go) so long-running commands (backup, restore, generate, scan)
// can stop in-flight `docker`/`rclone` child processes on Ctrl+C instead
// of leaving them running after dockvault itself exits.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, g Global, args []string) error
}

// registry is populated by init() in each subcommand's file (scan.go,
// generate.go, ...). Keeping registration next to each handler means a new
// command only needs to touch one file, not this one.
var registry = map[string]*command{}

func register(c *command) {
	if _, exists := registry[c.name]; exists {
		panic("dockvault: duplicate command registered: " + c.name)
	}
	registry[c.name] = c
}

// Execute is the single entry point called from main(). args is
// os.Args[1:]; ctx should be cancelled on SIGINT/SIGTERM (see main.go).
func Execute(ctx context.Context, args []string) error {
	g := defaultGlobal()

	fs := flag.NewFlagSet("dockvault", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&g.Home, "home", g.Home, "override $DOCKVAULT_HOME (default: ~/dockvault_scripts)")
	fs.StringVar(&g.RcloneRemote, "rclone-remote", g.RcloneRemote, "override rclone remote name (default: dockvault_backup)")
	fs.StringVar(&g.Environment, "environment", g.Environment, "override the remote path's <environment> segment (default: stage)")
	fs.BoolVar(&g.DryRun, "dry-run", g.DryRun, "don't write files, don't execute backups")
	fs.BoolVar(&g.Verbose, "verbose", g.Verbose, "verbose logging")
	fs.BoolVar(&g.ShowVersion, "version", g.ShowVersion, "print version and exit")
	fs.Usage = func() { printUsage(fs) }

	// Global flags are only recognized before the subcommand name.
	// flag.FlagSet.Parse already stops at the first non-flag argument on
	// its own (correctly consuming each flag's value token along the way,
	// e.g. "--environment stage"), so no manual pre-splitting is needed -
	// an earlier hand-rolled version of this split naively broke on any
	// two-token "--flag value" global flag.
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()

	if g.ShowVersion {
		fmt.Println(version.String())
		return nil
	}

	if len(rest) == 0 {
		printUsage(fs)
		return nil
	}

	name := rest[0]
	if name == "help" || name == "-h" || name == "--help" {
		printUsage(fs)
		return nil
	}

	cmd, ok := registry[name]
	if !ok {
		printUsage(fs)
		return fmt.Errorf("unknown command %q", name)
	}
	if err := cmd.run(ctx, g, rest[1:]); err != nil {
		// `dockvault <command> --help` intentionally exits 0: it's a
		// successful request for help, not a failure, even though
		// flag.FlagSet reports it as an error internally.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return nil
}

func printUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "dockvault - Docker volume backup orchestration")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  dockvault [global flags] <command> [command flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")

	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", n, registry[n].summary)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Global flags:")
	fs.PrintDefaults()
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Run 'dockvault <command> --help' for command-specific flags.")
}
