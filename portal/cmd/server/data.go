package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/datax"
	"github.com/mayiants/ai-portal-go/internal/db"
	"go.uber.org/zap"
)

// runDataCLI dispatches the `portal data ...` subcommands. It is invoked from
// main when os.Args[1] == "data" and never starts the HTTP server.
func runDataCLI(args []string) {
	if len(args) == 0 {
		dataUsage()
		os.Exit(2)
	}

	logger, _ := zap.NewDevelopment()
	defer func() { _ = logger.Sync() }()

	switch args[0] {
	case "export":
		runDataExport(logger, args[1:])
	case "import":
		runDataImport(logger, args[1:])
	case "help", "-h", "--help":
		dataUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown data subcommand %q\n\n", args[0])
		dataUsage()
		os.Exit(2)
	}
}

func runDataExport(logger *zap.Logger, args []string) {
	fs := flag.NewFlagSet("data export", flag.ExitOnError)
	out := fs.String("out", "", "output directory (required)")
	entities := fs.String("entities", strings.Join(datax.CoreEntities, ","), "comma-separated entities to export")
	since := fs.String("since", "", "only export rows with created_at after YYYY-MM-DD")
	_ = fs.Parse(args)

	if strings.TrimSpace(*out) == "" {
		fmt.Fprintln(os.Stderr, "data export: --out is required")
		fs.PrintDefaults()
		os.Exit(2)
	}

	database := connectDataDB(logger)
	defer database.Close()

	opts := datax.ExportOptions{
		OutDir:   *out,
		Entities: datax.ParseEntityList(*entities),
		Since:    *since,
	}
	if _, err := datax.Export(context.Background(), database.Pool, logger, opts); err != nil {
		logger.Fatal("data export failed", zap.Error(err))
	}
}

func runDataImport(logger *zap.Logger, args []string) {
	fs := flag.NewFlagSet("data import", flag.ExitOnError)
	in := fs.String("in", "", "input directory produced by `data export` (required)")
	mode := fs.String("mode", datax.ModeUpsert, "conflict mode: upsert or skip")
	dryRun := fs.Bool("dry-run", false, "report affected row counts without writing")
	entities := fs.String("entities", strings.Join(datax.CoreEntities, ","), "comma-separated entities to import")
	_ = fs.Parse(args)

	if strings.TrimSpace(*in) == "" {
		fmt.Fprintln(os.Stderr, "data import: --in is required")
		fs.PrintDefaults()
		os.Exit(2)
	}

	database := connectDataDB(logger)
	defer database.Close()

	opts := datax.ImportOptions{
		InDir:    *in,
		Mode:     *mode,
		DryRun:   *dryRun,
		Entities: datax.ParseEntityList(*entities),
	}
	if _, err := datax.Import(context.Background(), database.Pool, logger, opts); err != nil {
		logger.Fatal("data import failed", zap.Error(err))
	}
}

// connectDataDB opens the database using the same environment variables as the
// server, without running schema init, migrations, seeds, or the HTTP stack.
func connectDataDB(logger *zap.Logger) *db.DB {
	cfg := config.Load()
	database, err := db.New(cfg, logger)
	if err != nil {
		logger.Fatal("data: failed to connect to database", zap.Error(err))
	}
	return database
}

func dataUsage() {
	fmt.Fprint(os.Stderr, `Usage: portal data <command> [flags]

Commands:
  export   Dump core master data to JSON files + manifest.json
  import   Load core master data from a directory produced by export

Examples:
  portal data export --entities users,enterprises,api_keys --out ./backup-20260709 --since 2026-07-01
  portal data import --in ./backup-20260709 --mode upsert --dry-run
  portal data import --in ./backup-20260709 --mode skip

Run "portal data <command> -h" for command flags.
`)
}
