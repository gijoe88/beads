# Federation Setup Guide

Federation enables peer-to-peer synchronization of beads databases between
multiple machines or teams using Dolt remotes. Each installation maintains its
own database while sharing work items with configured peers.

## Overview

Federation uses Dolt's distributed version control capabilities to sync issue
data between independent teams or locations. Key benefits:

- **Peer-to-peer**: No central server required; each installation is autonomous
- **Database-native versioning**: Built on Dolt's version control, not file exports
- **Flexible infrastructure**: Works with DoltHub, S3, GCS, HTTP remotes, local paths, or SSH
- **Data sovereignty**: Configurable tiers for compliance (GDPR, regional laws)
- **Auto-bootstrap**: New contributors can clone and sync with minimal setup

## Prerequisites

1. **Dolt backend**: Federation requires the Dolt storage backend (the only supported backend)
2. **Dolt server**: Federation operations use the Dolt SQL server (`bd dolt start`)

## Configuration

### Federation Config in .beads/config.yaml

Add federation configuration to your project's `.beads/config.yaml`:

```yaml
federation:
  remote: dolthub://myorg/beads   # Or http://dolt-server:8080/beads
  name: central                   # Remote name (prevents auto-push)
```

**Key settings:**

- `federation.remote`: URL of the central Dolt remote server
- `federation.name`: Name for the Dolt remote (default: `origin`)
  - Use a non-`origin` name like `central` to prevent Dolt from auto-pushing on every commit
  - This gives you manual control over when syncs happen

### Data Sovereignty Tiers

| Tier | Description | Use Case |
|------|-------------|----------|
| T1 | No restrictions | Public data |
| T2 | Organization-level | Regional/company compliance |
| T3 | Pseudonymous | Identifiers removed |
| T4 | Anonymous | Maximum privacy |

Set via config or environment variable:
```bash
export BD_FEDERATION_SOVEREIGNTY="T2"
```

## Federation Commands

### Adding Peers

Use `bd federation add-peer` to register remote peers:

```bash
bd federation add-peer <name> <endpoint> [--user <username>] [--password <password>]
```

**Examples:**

```bash
# Add a peer with password (prompts for password)
bd federation add-peer central http://dolt-server:8080/beads --user sync-bot -p -

# Add a peer with empty password (for passwordless servers)
bd federation add-peer central http://dolt-server:8080/beads --user root --password=""

# Add a peer on DoltHub
bd federation add-peer staging dolthub://myorg/staging-beads

# Add cloud storage peers
bd federation add-peer backup gs://mybucket/beads-backup
bd federation add-peer backup-s3 s3://mybucket/beads-backup

# Add a local backup
bd federation add-peer local file:///home/user/beads-backup
```

**Password handling:**
- `-p -` prompts for password interactively
- `--password=""` explicitly sets an empty password
- Credentials are AES-256-GCM encrypted and stored in the `federation_peers` table

### Syncing with Peers

```bash
# Sync with a specific peer
bd federation sync --peer central

# Sync with conflict resolution strategy
bd federation sync --peer central --strategy ours     # Keep local changes
bd federation sync --peer central --strategy theirs   # Keep remote changes
```

**Sync workflow:**
1. Fetches changes from the remote peer
2. Merges remote changes with local
3. Pushes merged result back to peer

**Conflict strategies:**
- `newest` (default): Use the version with the most recent timestamp
- `ours`: Always prefer local changes
- `theirs`: Always prefer remote changes
- `manual`: Leave conflicts for manual resolution

### Checking Status

```bash
# Check status of a specific peer
bd federation status --peer central

# Check all peers
bd federation status
```

**Output example:**
```
🌐 Federation Status:

  central  dolthub://myorg/beads
    ✓ Reachable
    Ahead:  0 commits
    Behind: 0 commits
    Last sync: 2026-03-11 09:48:56
```

### Listing Peers

```bash
bd federation list-peers --json
```

## Workflows

### Setting Up a New Project with Federation

**On the primary machine (owner):**

```bash
# 1. Initialize the project
mkdir my-project && cd my-project
git init
bd init --prefix myproj

# 2. Configure federation in .beads/config.yaml
cat >> .beads/config.yaml << EOF
federation:
  remote: dolthub://myorg/my-project-beads
  name: central
EOF

# 3. Create some issues
bd create "First issue" -p 1 --json

# 4. Add peer credentials
bd federation add-peer central --user beads -p -

# 5. Push to create remote database
bd dolt push --set-upstream central main

# 6. Commit config to repo
git add .beads/config.yaml && git commit -m "Add federation config"
git push
```

### Onboarding a New Contributor (Clone Bootstrap)

When someone clones a repository that has federation configured:

```bash
# 1. Clone the project repo
git clone <project-url> my-project
cd my-project

# 2. Initialize beads with credentials (auto-bootstraps from federation.remote)
DOLT_REMOTE_PASSWORD=<password> bd init --prefix myproj --user beads
```

The `bd init` command automatically:
- Reads `federation.remote` from `.beads/config.yaml`
- Authenticates `dolt clone` using `--user` flag and `DOLT_REMOTE_PASSWORD` env
- Creates a remote named "central" (not "origin") to prevent auto-push
- **Auto-registers the federation peer** with your credentials

```bash
# 3. Sync and start working (no need for bd federation add-peer!)
bd federation sync --peer central
bd ready
```

**Note:** If you don't provide credentials during `bd init`, you'll need to run
`bd federation add-peer central --user beads -p -` before you can sync.

### Daily Sync Workflow

```bash
# Before starting work - pull latest changes
bd federation sync --peer central

# Make changes
bd create "New feature" -p 1
bd update bd-abc --claim
# ... work ...

# After making changes - push to share
bd federation sync --peer central

# If there are conflicts
bd federation sync --peer central --strategy ours
```

## Server Configuration

### Required Permissions on Remote Dolt Server

For federation to work, the remote Dolt server needs appropriate grants:

```sql
-- On the remote Dolt server
CREATE USER IF NOT EXISTS 'beads'@'%' IDENTIFIED BY '<secure-password>';
GRANT ALL PRIVILEGES ON beads*.* TO 'beads'@'%';
GRANT CLONE_ADMIN ON *.* TO 'beads'@'%';
GRANT SuperUser ON *.* TO 'beads'@'%';  -- Required for HTTP push operations
```

**Why SuperUser is required for push:**

Dolt's HTTP remote API uses gRPC with two privilege tiers:
- **CLONE_ADMIN**: Required for clone/fetch operations
- **SuperUser**: Required for push operations (GetUploadLocations, AddTableFiles, Commit)

This is a Dolt design limitation - no intermediate "push-only" privilege exists.

### Dolt CLI Authentication

**Important:** The `DOLT_REMOTE_USER` environment variable is **NOT used** by dolt CLI.
You must use the `--user` flag:

```bash
# Correct usage
DOLT_REMOTE_PASSWORD=xxx dolt push --user beads central main
DOLT_REMOTE_PASSWORD=xxx dolt fetch --user beads central
DOLT_REMOTE_PASSWORD=xxx dolt clone --user beads http://server/db

# Incorrect (DOLT_REMOTE_USER is ignored)
DOLT_REMOTE_USER=beads DOLT_REMOTE_PASSWORD=xxx dolt push central main
```

Beads handles this automatically when you store credentials via `bd federation add-peer`.

## What to Commit to the Project Repo

**TRACKED (committed to git):**
- `.beads/config.yaml` - Project settings including federation config
- `.beads/metadata.json` - Database name and backend config
- `.beads/hooks/*` - Git hooks
- `.beads/README.md` - Documentation
- `.beads/.gitignore` - Ignore rules for runtime files

**NOT TRACKED (in .gitignore):**
- `.beads/dolt/` - Local Dolt database (synced via federation)
- `.beads/dolt-server.*` - Server runtime files
- `.beads/ephemeral.sqlite3` - Ephemeral store
- `.beads/backup/` - Local backups

The `.beads/dolt/` directory contains your local Dolt database. This is NOT
committed to git - it's synced via federation instead. Each machine has its
own `.beads/dolt/` that syncs with the central server.

## Troubleshooting

### "API Authorization Failure: user has not been granted CLONE_ADMIN access"

The remote server requires CLONE_ADMIN privilege for clone/fetch operations.

**Fix:** Grant CLONE_ADMIN to the user on the remote server:
```sql
GRANT CLONE_ADMIN ON *.* TO 'beads'@'%';
```

### "API Authorization Failure: user has not been granted SuperUser access"

The remote server requires SuperUser privilege for push operations.

**Fix:** Grant SuperUser to the user on the remote server:
```sql
GRANT SuperUser ON *.* TO 'beads'@'%';
```

### "merge failed: local changes would be stomped"

Local uncommitted changes conflict with incoming merge.

**Fix:** Either commit your local changes first, or use a conflict strategy:
```bash
bd federation sync --peer central --strategy ours
```

### "merge failed: no common ancestor"

The local and remote databases have unrelated histories. This happens when:
- Two separate CLI repos existed with unrelated histories
- The database name in `metadata.json` doesn't match the actual folder

**Fix:**
1. Verify the database name matches the folder:
   ```bash
   cat .beads/metadata.json | grep dolt_database
   ls .beads/dolt/
   ```
2. If mismatched, rename the folder to match `metadata.json`
3. Force push from SQL server:
   ```bash
   mysql -h 127.0.0.1 -P <port> -u root <dbname> -e "CALL DOLT_PUSH('--force', 'central', 'main');"
   ```

### "Sync: not fetched yet" after successful sync

This was a bug in older versions where Dolt SQL didn't support parameter binding
inside `AS OF` clauses. Upgrade to the latest version.

### "no store available"

The federation command was incorrectly classified as not needing database access.

**Fix:** Ensure you're using the latest version where federation subcommands
are properly handled.

## Architecture Notes

### Three Dolt Repositories

Beads federation involves three logical components:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Remote (Central Server)                                                    │
│  Location: dolthub://myorg/beads or http://dolt-server:8080                │
│  Purpose: Central federation hub for sync                                   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    ↑↓
                              federation sync
                                    ↑↓
┌─────────────────────────────────────────────────────────────────────────────┐
│  Local SQL Server (bd dolt start)                                          │
│  Location: Port derived from project path                                  │
│  Data dir: .beads/dolt/                                                     │
│  Purpose: SQL access for bd commands                                        │
└─────────────────────────────────────────────────────────────────────────────┘
                                    ↑↓
                          SAME .dolt DATA FILES
                                    ↑↓
┌─────────────────────────────────────────────────────────────────────────────┐
│  CLI Repo (.beads/dolt/<dbname>/)                                          │
│  Purpose: Used by `dolt` CLI commands, git-protocol operations             │
│  NOTE: SQL Server and CLI share the same .dolt data!                       │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Key insight:** SQL Server and CLI repo share the same `.dolt` directory. The
"CLI repo" folder exists so `dolt` CLI commands can find remotes and run
operations that require CLI (git-protocol URLs).

### Multi-Repo Support

Issues track their `SourceSystem` to identify which federated system created
them. This enables proper attribution and trust chains across organizations.

## Reference

- **Configuration:** See `docs/CONFIG.md` for all federation settings
- **Source:** `cmd/bd/federation.go`
- **Storage interfaces:** `internal/storage/versioned.go`
- **Dolt implementation:** `internal/storage/dolt/federation.go`
- **Credential handling:** `internal/storage/dolt/credentials.go`
