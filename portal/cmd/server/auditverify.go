package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/routes"
	"github.com/mayiants/ai-portal-go/pkg/auditchain"
	"go.uber.org/zap"
)

// runVerifyAuditCLI implements `portal verify-audit`, an operator/cron entry
// point for the same integrity check as GET /api/admin/audit/verify. It
// connects with the DB settings from the environment, prints one JSON object
// per table and exits 1 when any table fails (or cannot be checked).
func runVerifyAuditCLI(args []string) {
	logger := zap.NewNop()
	cfg := config.Load()
	database, err := db.New(cfg, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-audit: cannot connect to database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()
	os.Exit(verifyAuditCommand(context.Background(), database, cfg.Auth.EncryptionKey, args, os.Stdout, os.Stderr))
}

// verifyAuditCommand is the testable core of the CLI. Exit codes: 0 all tables
// verified, 1 verification failed or errored, 2 bad usage.
func verifyAuditCommand(ctx context.Context, database *db.DB, encryptionKey string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	table := fs.String("table", "all", "audit_logs, enterprise_audit_log or all")
	from := fs.String("from", "", "only verify rows created at/after this time (YYYY-MM-DD or RFC3339)")
	to := fs.String("to", "", "only verify rows created before this time (YYYY-MM-DD or RFC3339)")
	usage := fs.Bool("usage", false, "also recompute sealed usage_records day digests")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	tables := []string{*table}
	if strings.EqualFold(*table, "all") {
		tables = []string{auditchain.TableAuditLogs, auditchain.TableEnterpriseAuditLog}
	}

	exit := 0
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	for _, t := range tables {
		res, _, err := routes.VerifyAuditTable(ctx, database, encryptionKey, t, *from, *to, *usage)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "verify-audit: %s: %v\n", t, err)
			exit = 1
			continue
		}
		_ = enc.Encode(res)
		if !res.OK {
			_, _ = fmt.Fprintf(stderr, "verify-audit: %s FAILED: %s\n", t, res.Reason)
			exit = 1
		}
	}
	return exit
}
