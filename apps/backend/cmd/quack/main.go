// Command quack runs the Quack moderation bot and its operator tools.
//
// Usage:
//
//	quack [serve] [-config file]
//	quack migrate [-config file] [-drop-all] [up|down]
//	quack import-v4 import|rollback|check-scope [flags]
//
// Settings come from code defaults, then the TOML file, then QUACK_* env vars.
// See docs/configuration.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/logging"
	quackruntime "github.com/quackdiscord/bot/internal/runtime"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/v4import"
)

const usage = `Usage: quack <command> [flags]

Commands:
  serve                   run the bot and HTTP API (the default)
  migrate [up|down]       apply pending migrations, or roll back the newest one
                          (rolling back the baseline needs -drop-all)
  import-v4 import        import v4 case history from a JSONL file
  import-v4 rollback      undo one import batch
  import-v4 check-scope   check that v4 and v5 command names don't collide

Commands that read settings take -config <file>. Flags go before arguments.
Settings come from defaults, then the TOML file (default quack.toml, or
$QUACK_CONFIG), then QUACK_* environment variables.
Run "quack <command> -h" for a command's flags.
`

// errReported marks a usage or config mistake whose explanation has already
// been printed to stderr, so main exits without logging it a second time.
var errReported = errors.New("already reported")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
	case errors.Is(err, errReported):
		os.Exit(2)
	default:
		slog.Error("Quack failed", "error", err)
		os.Exit(1)
	}
}

// run dispatches to a subcommand. With no arguments, or with only flags, it
// serves, so a bare `quack` in a container starts the bot.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	command := "serve"
	if len(args) > 0 && (!strings.HasPrefix(args[0], "-") || isHelpFlag(args[0])) {
		command, args = args[0], args[1:]
	}
	if isHelpFlag(command) {
		command = "help"
	}
	switch command {
	case "serve":
		return serve(ctx, args, stderr)
	case "migrate":
		return migrate(args, stderr)
	case "import-v4":
		return importV4(ctx, args, stdout, stderr)
	case "help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprintf(stderr, "quack: unknown command %q\n\n%s", command, usage)
		return errReported
	}
}

// isHelpFlag reports whether arg asks for the top-level usage.
func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// newFlagSet returns a flag set for one subcommand with the shared -config
// flag already defined.
func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("quack "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "", "TOML config `file` (default $QUACK_CONFIG or quack.toml)")
	return fs, path
}

// parse parses args into fs. The flag package has already printed any error
// and the flag list, so parse only translates the error for main.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errReported
	}
	return nil
}

// loadConfig loads the configuration and installs the process logger. This
// is the only place the default logger is set.
func loadConfig(path string, stderr io.Writer) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, err
	}
	logger, err := logging.New(stderr, cfg.Environment == "dev", cfg.Log.Level)
	if err != nil {
		return config.Config{}, err
	}
	slog.SetDefault(logger)
	return cfg, nil
}

// serve runs the bot, workers, and HTTP API until ctx is cancelled.
func serve(ctx context.Context, args []string, stderr io.Writer) error {
	fs, path := newFlagSet("serve", stderr)
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "quack serve: unexpected argument %q\n", fs.Arg(0))
		return errReported
	}
	cfg, err := loadConfig(*path, stderr)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "quack: invalid config:\n%v\n", err)
		return errReported
	}
	return quackruntime.Run(ctx, cfg)
}

// migrate applies every pending migration, or with "down" rolls back the
// newest one. Rolling back the baseline drops every table, so it also needs
// -drop-all. It touches only MySQL.
func migrate(args []string, stderr io.Writer) error {
	fs, path := newFlagSet("migrate", stderr)
	dropAll := fs.Bool("drop-all", false, "allow \"down\" to roll back the baseline, dropping every table and all data")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: quack migrate [-config file] [-drop-all] [up|down]")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	direction := "up"
	switch {
	case fs.NArg() == 1 && (fs.Arg(0) == "up" || fs.Arg(0) == "down"):
		direction = fs.Arg(0)
	case fs.NArg() != 0:
		fs.Usage()
		return errReported
	}
	if *dropAll && direction != "down" {
		fmt.Fprintln(stderr, "quack migrate: -drop-all only applies to down")
		return errReported
	}
	cfg, err := loadConfig(*path, stderr)
	if err != nil {
		return err
	}
	return withStore(cfg, func(s *store.Store) error {
		if direction == "up" {
			if err := s.Migrate(); err != nil {
				return fmt.Errorf("apply migrations: %w", err)
			}
			return nil
		}
		err := s.Rollback(*dropAll)
		if errors.Is(err, store.ErrBaselineRollback) {
			fmt.Fprintln(stderr, "quack migrate: the newest migration is the baseline; rolling it back drops every table and all data.\nRerun with -drop-all to do that.")
			return errReported
		}
		if err != nil {
			return fmt.Errorf("roll back latest migration: %w", err)
		}
		return nil
	})
}

// importV4 runs one of the v4 history import operations.
func importV4(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, "Usage: quack import-v4 import|rollback|check-scope [flags]\n")
		return errReported
	}
	switch args[0] {
	case "import":
		return importV4Import(ctx, args[1:], stdout, stderr)
	case "rollback":
		return importV4Rollback(ctx, args[1:], stderr)
	case "check-scope":
		return checkScope(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "quack import-v4: unknown operation %q\n", args[0])
		return errReported
	}
}

// importV4Import imports a JSONL export into one v5 guild and prints the
// import report as JSON. The report is printed even when the import fails,
// since it says which records were rejected.
func importV4Import(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, path := newFlagSet("import-v4 import", stderr)
	file := fs.String("file", "", "versioned JSONL `export` to import (required)")
	source := fs.String("source", "", "stable source name, used to detect re-imports")
	guild := fs.String("guild", "", "v5 guild ULID")
	actor := fs.String("actor", "", "operator Discord user ID")
	dryRun := fs.Bool("dry-run", false, "validate and report without writing")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *file == "" {
		fmt.Fprintln(stderr, "quack import-v4 import: -file is required")
		return errReported
	}
	cfg, err := loadConfig(*path, stderr)
	if err != nil {
		return err
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	return withStore(cfg, func(s *store.Store) error {
		report, importErr := v4import.New(s).Import(ctx, *source, *guild, *actor, input, *dryRun)
		if report != nil {
			if err := json.NewEncoder(stdout).Encode(report); err != nil {
				return errors.Join(importErr, err)
			}
		}
		return importErr
	})
}

// importV4Rollback removes everything one import batch created.
func importV4Rollback(ctx context.Context, args []string, stderr io.Writer) error {
	fs, path := newFlagSet("import-v4 rollback", stderr)
	guild := fs.String("guild", "", "v5 guild ULID (required)")
	batch := fs.String("batch", "", "import batch ID from the import report (required)")
	actor := fs.String("actor", "", "operator Discord user ID (required)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *guild == "" || *batch == "" || *actor == "" {
		fmt.Fprintln(stderr, "quack import-v4 rollback: -guild, -batch, and -actor are required")
		return errReported
	}
	cfg, err := loadConfig(*path, stderr)
	if err != nil {
		return err
	}
	return withStore(cfg, func(s *store.Store) error {
		return v4import.New(s).Rollback(ctx, *guild, *batch, *actor)
	})
}

// checkScope fails if the v4 and v5 bots would register colliding commands.
// It needs no config or database.
func checkScope(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("quack import-v4 check-scope", flag.ContinueOnError)
	fs.SetOutput(stderr)
	v4 := fs.String("v4", "", "comma-separated v4 command names")
	v5 := fs.String("v5", "", "comma-separated v5 command names")
	after := fs.Bool("after-migration", false, "also require that v4's direct moderation commands are gone")
	if err := parse(fs, args); err != nil {
		return err
	}
	return v4import.ValidateCommandScopes(splitList(*v4), splitList(*v5), *after)
}

// withStore opens MySQL (without Redis), runs fn, and closes the connection.
func withStore(cfg config.Config, fn func(*store.Store) error) error {
	if strings.TrimSpace(cfg.Database.DSN) == "" {
		return errors.New("database.dsn (QUACK_DATABASE_DSN) is required")
	}
	db, err := store.OpenMySQL(cfg.Database.DSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database handle: %w", err)
	}
	defer sqlDB.Close()
	return fn(store.New(db, nil))
}

// splitList splits a comma-separated flag value; an empty value is no names.
func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
