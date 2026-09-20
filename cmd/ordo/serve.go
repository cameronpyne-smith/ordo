package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/server"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

const backupsKept = 30

func newServeCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the ordo daemon: HTTP API over the tailnet",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			dbPath, err := cfg.DBPath()
			if err != nil {
				return err
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))

			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			sum, err := st.Summary()
			if err != nil {
				return err
			}
			log.Info("store opened", "path", dbPath, "open", sum.Open, "done", sum.Done, "overdue", sum.Overdue)
			if cfg.Token == "" {
				log.Warn("no token set — the API is unauthenticated")
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			if cfg.BackupDir != "" {
				go runBackups(ctx, st, cfg.BackupDir, log)
				log.Info("nightly backups enabled", "dir", cfg.BackupDir, "keep", backupsKept)
			} else {
				log.Warn("backup_dir not set — no snapshots are being taken")
			}

			srv := &http.Server{Addr: cfg.Bind, Handler: server.New(st, cfg.Token)}
			errCh := make(chan error, 1)
			go func() {
				log.Info("listening", "addr", cfg.Bind)
				errCh <- srv.ListenAndServe()
			}()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				log.Info("shutting down")
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return srv.Shutdown(shutdownCtx)
			}
		},
	}
}

// runBackups snapshots the database at 03:00 local time each night. A failure
// is logged and retried the next night; it never stops the daemon.
func runBackups(ctx context.Context, st *store.Store, dir string, log *slog.Logger) {
	for {
		wait := time.Until(nextBackupTime(store.Now()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		path, err := st.Backup(dir, backupsKept)
		if err != nil {
			log.Error("backup failed", "error", err)
			continue
		}
		log.Info("backup written", "path", path)
	}
}

func nextBackupTime(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
