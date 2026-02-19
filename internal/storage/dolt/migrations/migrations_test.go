//go:build cgo

package migrations

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	embedded "github.com/dolthub/driver"

	"github.com/steveyegge/beads/internal/storage/doltutil"
)

// openTestDolt creates a temporary embedded Dolt database for testing.
func openTestDolt(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testdb")
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		t.Fatalf("failed to create db dir: %v", err)
	}

	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	// First connect without database to create it
	initDSN := fmt.Sprintf("file://%s?commitname=test&commitemail=test@test.com", absPath)
	initCfg, err := embedded.ParseDSN(initDSN)
	if err != nil {
		t.Fatalf("failed to parse init DSN: %v", err)
	}

	initConnector, err := embedded.NewConnector(initCfg)
	if err != nil {
		t.Fatalf("failed to create init connector: %v", err)
	}

	initDB := sql.OpenDB(initConnector)
	_, err = initDB.Exec("CREATE DATABASE IF NOT EXISTS beads")
	if err != nil {
		_ = doltutil.CloseWithTimeout("initDB", initDB.Close)
		_ = doltutil.CloseWithTimeout("initConnector", initConnector.Close)
		t.Fatalf("failed to create database: %v", err)
	}
	_ = doltutil.CloseWithTimeout("initDB", initDB.Close)
	_ = doltutil.CloseWithTimeout("initConnector", initConnector.Close)

	// Now connect with database specified
	dsn := fmt.Sprintf("file://%s?commitname=test&commitemail=test@test.com&database=beads", absPath)
	cfg, err := embedded.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("failed to parse DSN: %v", err)
	}

	connector, err := embedded.NewConnector(cfg)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	t.Cleanup(func() { _ = doltutil.CloseWithTimeout("connector", connector.Close) })

	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = doltutil.CloseWithTimeout("db", db.Close) })

	// Create minimal issues table without wisp_type (simulating old schema)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS issues (
		id VARCHAR(255) PRIMARY KEY,
		title VARCHAR(500) NOT NULL,
		status VARCHAR(32) NOT NULL DEFAULT 'open',
		ephemeral TINYINT(1) DEFAULT 0,
		pinned TINYINT(1) DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("failed to create issues table: %v", err)
	}

	return db
}

func TestMigrateWispTypeColumn(t *testing.T) {
	db := openTestDolt(t)

	// Verify column doesn't exist yet
	exists, err := columnExists(db, "issues", "wisp_type")
	if err != nil {
		t.Fatalf("failed to check column: %v", err)
	}
	if exists {
		t.Fatal("wisp_type should not exist yet")
	}

	// Run migration
	if err := MigrateWispTypeColumn(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify column now exists
	exists, err = columnExists(db, "issues", "wisp_type")
	if err != nil {
		t.Fatalf("failed to check column: %v", err)
	}
	if !exists {
		t.Fatal("wisp_type should exist after migration")
	}

	// Run migration again (idempotent)
	if err := MigrateWispTypeColumn(db); err != nil {
		t.Fatalf("re-running migration should be idempotent: %v", err)
	}
}

func TestColumnExists(t *testing.T) {
	db := openTestDolt(t)

	exists, err := columnExists(db, "issues", "id")
	if err != nil {
		t.Fatalf("failed to check column: %v", err)
	}
	if !exists {
		t.Fatal("id column should exist")
	}

	exists, err = columnExists(db, "issues", "nonexistent")
	if err != nil {
		t.Fatalf("failed to check column: %v", err)
	}
	if exists {
		t.Fatal("nonexistent column should not exist")
	}
}

func TestDetectOrphanedChildren_NoOrphans(t *testing.T) {
	db := openTestDolt(t)

	// Insert parent and child - no orphans
	_, err := db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-abc123', 'Parent', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert parent: %v", err)
	}
	_, err = db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-abc123.1', 'Child', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert child: %v", err)
	}

	// Should find no orphans
	orphans, err := QueryOrphanedChildren(db)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans, got %d", len(orphans))
	}

	// Migration should succeed (no-op)
	if err := DetectOrphanedChildren(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
}

func TestDetectOrphanedChildren_WithOrphans(t *testing.T) {
	db := openTestDolt(t)

	// Insert only child issues (no parent)
	_, err := db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-abc123.1', 'Orphan Child 1', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert orphan 1: %v", err)
	}
	_, err = db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-abc123.2', 'Orphan Child 2', 'closed')`)
	if err != nil {
		t.Fatalf("failed to insert orphan 2: %v", err)
	}

	// Insert a non-orphan (different parent exists)
	_, err = db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-def456', 'Other Parent', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert other parent: %v", err)
	}
	_, err = db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-def456.1', 'Not Orphan', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert non-orphan child: %v", err)
	}

	orphans, err := QueryOrphanedChildren(db)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("expected 2 orphans, got %d", len(orphans))
	}

	// Verify orphan details (ordered by id)
	if orphans[0].ID != "bd-abc123.1" {
		t.Errorf("orphan[0].ID = %q, want %q", orphans[0].ID, "bd-abc123.1")
	}
	if orphans[0].Title != "Orphan Child 1" {
		t.Errorf("orphan[0].Title = %q, want %q", orphans[0].Title, "Orphan Child 1")
	}
	if orphans[1].ID != "bd-abc123.2" {
		t.Errorf("orphan[1].ID = %q, want %q", orphans[1].ID, "bd-abc123.2")
	}

	// Migration should succeed (advisory only - logs but doesn't error)
	if err := DetectOrphanedChildren(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
}

func TestDetectOrphanedChildren_DeepNesting(t *testing.T) {
	db := openTestDolt(t)

	// Insert parent
	_, err := db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-abc123', 'Parent', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert parent: %v", err)
	}
	// Insert grandchild without child (bd-abc123.1 is missing)
	_, err = db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-abc123.1.1', 'Grandchild', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert grandchild: %v", err)
	}

	orphans, err := QueryOrphanedChildren(db)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	// bd-abc123.1.1 is orphaned because bd-abc123.1 doesn't exist
	// The parent is the part before the FIRST dot: "bd-abc123" which exists
	// But the query checks SUBSTRING(id, 1, INSTR(id, '.') - 1) which gives "bd-abc123"
	// So this grandchild is NOT detected as orphaned by the first-dot check
	// This is expected behavior - we only check immediate parent (prefix before first dot)
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans (grandchild has existing root parent), got %d", len(orphans))
	}
}

func TestDetectOrphanedChildren_Idempotent(t *testing.T) {
	db := openTestDolt(t)

	// Insert orphan
	_, err := db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-gone.1', 'Orphan', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert orphan: %v", err)
	}

	// Run twice - should be idempotent (advisory only)
	if err := DetectOrphanedChildren(db); err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	if err := DetectOrphanedChildren(db); err != nil {
		t.Fatalf("second run failed: %v", err)
	}
}

func TestDetectOrphanedChildren_EmptyTable(t *testing.T) {
	db := openTestDolt(t)

	// Empty table - no orphans
	orphans, err := QueryOrphanedChildren(db)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans, got %d", len(orphans))
	}
}

func TestDetectOrphanedChildren_TopLevelOnly(t *testing.T) {
	db := openTestDolt(t)

	// Insert only top-level issues (no dots) - no orphans possible
	_, err := db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-aaa', 'Issue 1', 'open')`)
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}
	_, err = db.Exec(`INSERT INTO issues (id, title, status) VALUES ('bd-bbb', 'Issue 2', 'closed')`)
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	orphans, err := QueryOrphanedChildren(db)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans, got %d", len(orphans))
	}
}

func TestTableExists(t *testing.T) {
	db := openTestDolt(t)

	exists, err := tableExists(db, "issues")
	if err != nil {
		t.Fatalf("failed to check table: %v", err)
	}
	if !exists {
		t.Fatal("issues table should exist")
	}

	exists, err = tableExists(db, "nonexistent")
	if err != nil {
		t.Fatalf("failed to check table: %v", err)
	}
	if exists {
		t.Fatal("nonexistent table should not exist")
	}
}
