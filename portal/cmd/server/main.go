package main

import (
	"context"
	"os"
	"sync"
)

func main() {
	// `portal data ...` is an operator CLI for master-data export/import. It
	// connects to the database and exits without starting the HTTP server.
	if len(os.Args) > 1 && os.Args[1] == "data" {
		runDataCLI(os.Args[2:])
		return
	}

	// `portal verify-audit` re-checks the audit hash chains and anchors (exit 1
	// on failure) and never starts the HTTP server; suitable for cron.
	if len(os.Args) > 1 && os.Args[1] == "verify-audit" {
		runVerifyAuditCLI(os.Args[2:])
		return
	}

	deps := bootstrap()
	defer func() { _ = deps.logger.Sync() }()
	defer deps.database.Close()

	// Cancellable context so shutdown can signal background tickers/workers
	// to stop before the database is closed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// WaitGroup tracks in-flight background tasks so shutdown can wait for
	// them (with a timeout) instead of closing the DB mid-write.
	var bgWg sync.WaitGroup

	router := setupRouter(deps)
	startBackgroundJobs(ctx, deps, &bgWg)

	srv := startServer(deps, router)
	waitForShutdown(deps, srv, cancel, &bgWg)
}
