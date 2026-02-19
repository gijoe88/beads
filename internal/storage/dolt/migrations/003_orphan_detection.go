package migrations

import (
	"database/sql"
	"fmt"
	"log"
)

// OrphanInfo holds information about an orphaned child issue.
type OrphanInfo struct {
	ID     string
	Title  string
	Status string
}

// DetectOrphanedChildren finds child issues (IDs containing '.') whose parent
// ID no longer exists in the issues table. This can happen when a parent is
// deleted but its children remain.
//
// This migration is advisory: it logs orphans but does not modify data.
// Use RepairOrphanedChildren to take action on detected orphans.
func DetectOrphanedChildren(db *sql.DB) error {
	// Check if the issues table exists first
	exists, err := tableExists(db, "issues")
	if err != nil {
		return fmt.Errorf("failed to check issues table: %w", err)
	}
	if !exists {
		return nil
	}

	orphans, err := QueryOrphanedChildren(db)
	if err != nil {
		return fmt.Errorf("failed to detect orphaned children: %w", err)
	}

	if len(orphans) == 0 {
		return nil
	}

	log.Printf("orphan detection: found %d orphaned child issue(s):", len(orphans))
	for _, o := range orphans {
		log.Printf("  orphan: %s (title=%q, status=%s)", o.ID, o.Title, o.Status)
	}
	log.Printf("orphan detection: run 'bd doctor --fix' to repair orphaned children")

	return nil
}

// QueryOrphanedChildren returns all child issues whose parent does not exist.
// A child issue has an ID containing '.' (e.g., "bd-abc123.1").
// The parent ID is the portion before the first '.' (e.g., "bd-abc123").
func QueryOrphanedChildren(db *sql.DB) ([]OrphanInfo, error) {
	rows, err := db.Query(`
		SELECT id, title, status
		FROM issues
		WHERE INSTR(id, '.') > 0
		  AND SUBSTRING(id, 1, INSTR(id, '.') - 1) NOT IN (SELECT id FROM issues)
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("orphan query failed: %w", err)
	}
	defer rows.Close()

	var orphans []OrphanInfo
	for rows.Next() {
		var o OrphanInfo
		if err := rows.Scan(&o.ID, &o.Title, &o.Status); err != nil {
			return nil, fmt.Errorf("failed to scan orphan row: %w", err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orphan query iteration failed: %w", err)
	}

	return orphans, nil
}
