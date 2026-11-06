package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ai-proxy/ai-portal-go/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// startServer runs the HTTP server in a goroutine and returns the server
// instance so it can be shut down gracefully.
func startServer(deps *AppDeps, router *gin.Engine) *http.Server {
	addr := fmt.Sprintf(":%d", deps.cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      300 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		deps.logger.Info("Portal starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			deps.logger.Fatal("Server failed", zap.Error(err))
		}
	}()

	return srv
}

// waitForShutdown blocks until an interrupt signal is received, then performs a
// graceful shutdown of the HTTP server, background tasks, and the database
// connection. Background tasks are signalled to stop via bgCancel and awaited on
// bgWg (with a 30s timeout) before the DB is closed, so jobs mid-write (monthly
// billing reset, auto-renewals) are not disconnected.
func waitForShutdown(deps *AppDeps, srv *http.Server, bgCancel context.CancelFunc, bgWg *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	deps.logger.Info("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		deps.logger.Error("Server shutdown failed", zap.Error(err))
	}
	if err := telemetry.Shutdown(ctx); err != nil {
		deps.logger.Warn("Telemetry shutdown error", zap.Error(err))
	}

	// Signal background jobs to stop, then wait for them to finish so we do
	// not close the database while a job (e.g. monthly billing reset) is
	// mid-write. Bound the wait so a stuck job cannot hang shutdown forever.
	bgCancel()
	bgDone := make(chan struct{})
	go func() {
		bgWg.Wait()
		close(bgDone)
	}()
	select {
	case <-bgDone:
		deps.logger.Info("Background tasks stopped")
	case <-time.After(30 * time.Second):
		deps.logger.Warn("Timed out waiting for background tasks to stop; proceeding with DB close")
	}

	deps.auditLog.Stop(ctx)
	deps.database.Close()
	deps.logger.Info("Server stopped")
}
