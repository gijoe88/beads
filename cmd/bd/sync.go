package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/config"
)

// syncCmd commits pending Dolt changes and exports to JSONL.
// This ensures Dolt and JSONL stay in sync with git commits via pre-commit hook.
var syncCmd = &cobra.Command{
	Use:     "sync",
	GroupID: "sync",
	Short:   "Commit Dolt changes and export to JSONL",
	Long: `Sync ensures Dolt database state is committed and exported to JSONL.

This command:
1. Commits any pending Dolt changes (captures bd create/update/close operations)
2. Exports database to JSONL (for git-based workflows)
3. Pushes to Dolt remote if configured

Called by pre-commit hook to ensure Dolt commits when git commits.

For Dolt remote operations:
  bd dolt push     Push to Dolt remote
  bd dolt pull     Pull from Dolt remote

For data interchange:
  bd export        Export database to JSONL
  bd import        Import JSONL into database`,
	Run: func(_ *cobra.Command, _ []string) {
		// The global store is already opened by PersistentPreRun with the
		// access lock held. Use it directly instead of spawning a subprocess
		// (which would deadlock on the same lock).
		if store == nil {
			return // No database open, nothing to export
		}
		beadsDir := beads.FindBeadsDir()
		if beadsDir == "" {
			return
		}

		// First: Commit any pending Dolt changes
		// This ensures bd create/update/close operations are captured before export
		commitMsg := fmt.Sprintf("bd sync (auto-commit) by %s", getActor())
		if err := store.Commit(rootCtx, commitMsg); err != nil {
			// "nothing to commit" is expected when no changes - not an error
			if !isDoltNothingToCommit(err) {
				fmt.Fprintf(os.Stderr, "Warning: Dolt commit failed: %v\n", err)
			}
		}

		// In dolt-native mode, skip JSONL export — Dolt is the source of truth.
		// Only push to Dolt remote if configured.
		if config.GetSyncMode() == config.SyncModeDoltNative {
			if hasRemote, err := store.HasRemote(rootCtx, "origin"); err == nil && hasRemote {
				if err := store.Push(rootCtx); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: Dolt push failed: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "Pushed to Dolt git remote\n")
				}
			}
			return
		}

		jsonlPath := filepath.Join(beadsDir, "issues.jsonl")
		if err := exportToJSONLWithStore(rootCtx, store, jsonlPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: export failed: %v\n", err)
		}

		// Dolt-in-Git: if the Dolt store has a git remote configured,
		// push natively via DOLT_PUSH. This is additive — runs after
		// JSONL export succeeds (backward compat preserved).
		if hasRemote, err := store.HasRemote(rootCtx, "origin"); err == nil && hasRemote {
			if err := store.Push(rootCtx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Dolt push failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Pushed to Dolt git remote\n")
			}
		}

	},
}

func init() {
	// Keep all legacy flags so old invocations don't error out.
	syncCmd.Flags().StringP("message", "m", "", "Deprecated: no-op")
	syncCmd.Flags().Bool("dry-run", false, "Deprecated: no-op")
	syncCmd.Flags().Bool("no-push", false, "Deprecated: no-op")
	syncCmd.Flags().Bool("import", false, "Deprecated: use 'bd import' instead")
	syncCmd.Flags().Bool("import-only", false, "Deprecated: use 'bd import' instead")
	syncCmd.Flags().Bool("export", false, "Deprecated: use 'bd export' instead")
	syncCmd.Flags().Bool("flush-only", false, "Deprecated: no-op")
	syncCmd.Flags().Bool("pull", false, "Deprecated: use 'bd dolt pull' instead")
	syncCmd.Flags().Bool("no-git-history", false, "Deprecated: no-op")
	syncCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	rootCmd.AddCommand(syncCmd)
}
