package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/config"
	"github.com/cameronpyne-smith/ordo/internal/enrich"
	"github.com/cameronpyne-smith/ordo/internal/mnemo"
	"github.com/cameronpyne-smith/ordo/internal/ollama"
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

			worker := startEnrichment(ctx, st, cfg, log)
			vault := openVault(cfg, log)

			srv := &http.Server{Addr: cfg.Bind, Handler: server.New(server.Options{
				Store:  st,
				Token:  cfg.Token,
				Enrich: worker,
				Vault:  vault,
				Log:    log,
			})}
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

// startEnrichment brings up the background reader of new tasks, or reports
// why it is not running. It returns nil when there is no model configured,
// which every other part of the daemon is built to tolerate.
func startEnrichment(ctx context.Context, st *store.Store, cfg config.Config, log *slog.Logger) server.Enqueuer {
	if cfg.Ollama.URL == "" || cfg.Ollama.Model == "" {
		log.Warn("enrichment is off — set [ollama] url and model to have tasks read")
		return nil
	}
	model := ollama.New(cfg.Ollama.URL, cfg.Ollama.Model)
	worker := enrich.New(st, model, log)
	go worker.Run(ctx)
	// The check is only worth a log line, but it is the difference between
	// seeing the wrong model name at startup and wondering for a week why
	// nothing is being filled in.
	go func() {
		if err := model.Check(ctx); err != nil {
			log.Warn("ollama is not ready", "error", err)
			return
		}
		log.Info("enrichment ready", "url", cfg.Ollama.URL, "model", cfg.Ollama.Model)
	}()
	worker.Backlog()
	return worker
}

// openVault connects the read-only mnemo client, or reports that links are
// off. Nothing else in the daemon depends on it being there.
func openVault(cfg config.Config, log *slog.Logger) *mnemo.Client {
	if cfg.Mnemo.URL == "" {
		log.Warn("mnemo links are off — set [mnemo] url to link tasks to notes")
		return nil
	}
	log.Info("mnemo links enabled", "url", cfg.Mnemo.URL)
	return mnemo.New(cfg.Mnemo.URL, cfg.Mnemo.Token)
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
