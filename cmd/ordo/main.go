package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/config"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:           "ordo",
		Short:         "ordo orders what to do next, over its own store and mnemo",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          runTUI(&configPath),
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to config file")
	root.AddCommand(
		newInitCmd(&configPath),
		newServeCmd(&configPath),
		newListCmd(&configPath),
		newAddCmd(&configPath),
		newDoneCmd(&configPath),
		newSetCmd(&configPath),
		newUndoCmd(&configPath),
		newEnrichCmd(&configPath),
		newLinkCmd(&configPath),
		newUnlinkCmd(&configPath),
		newRelatedCmd(&configPath),
		newRemoveCmd(&configPath),
		newStatusCmd(&configPath),
		newBackupCmd(&configPath),
	)
	return root
}

func resolveConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return config.DefaultPath()
}

func loadConfig(path string) (config.Config, error) {
	resolved, err := resolveConfigPath(path)
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(resolved)
}

func newInitCmd(configPath *string) *cobra.Command {
	var db, backupDir, bind string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a config file, generating a bearer token if there is none",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveConfigPath(*configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				cfg = config.Default()
			}
			if bind != "" {
				cfg.Bind = bind
			}
			if db != "" {
				cfg.DB = db
			}
			if backupDir != "" {
				cfg.BackupDir = backupDir
			}
			if cfg.Token == "" {
				if cfg.Token, err = config.NewToken(); err != nil {
					return err
				}
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			dbPath, err := cfg.DBPath()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config written to %s\ndatabase %s\nlistening on %s once started\n",
				path, dbPath, cfg.Bind)
			return nil
		},
	}
	cmd.Flags().StringVar(&bind, "bind", "", "address the daemon listens on")
	cmd.Flags().StringVar(&db, "db", "", "path to the SQLite database")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "directory for nightly snapshots")
	return cmd
}
