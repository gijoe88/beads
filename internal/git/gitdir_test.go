package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupTestRepo creates a temporary git repository for testing.
func setupTestRepo(t *testing.T) (repoPath string, cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	repoPath = filepath.Join(tmpDir, "test-repo")
	if err := os.MkdirAll(repoPath, 0750); err != nil {
		t.Fatalf("Failed to create test repo directory: %v", err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = repoPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to init git repo: %v\nOutput: %s", err, string(output))
	}
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = repoPath
	_ = cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = repoPath
	_ = cmd.Run()
	beadsDir := filepath.Join(repoPath, ".beads")
	_ = os.MkdirAll(beadsDir, 0750)
	_ = os.WriteFile(filepath.Join(beadsDir, "test.jsonl"), []byte("test data\n"), 0644)
	_ = os.WriteFile(filepath.Join(repoPath, "other.txt"), []byte("other data\n"), 0644)
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = repoPath
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = repoPath
	_, _ = cmd.CombinedOutput()
	cleanup = func() {}
	return repoPath, cleanup
}

func TestGetGitHooksDirTildeExpansion(t *testing.T) {
	// Use an explicit temporary HOME so tilde expansion is deterministic
	// regardless of the environment (CI, containers, overridden HOME, etc.).
	fakeHome := t.TempDir()

	tests := []struct {
		name      string
		hooksPath string
		// wantDir is either an absolute path or "REPO_RELATIVE:" prefix
		// meaning the expected path is relative to the subtest's repo root.
		wantDir string
	}{
		{
			name:      "tilde with forward slash",
			hooksPath: "~/.githooks",
			wantDir:   filepath.Join(fakeHome, ".githooks"),
		},
		{
			name:      "tilde with backslash",
			hooksPath: `~\.githooks`,
			wantDir:   filepath.Join(fakeHome, ".githooks"),
		},
		{
			name:      "bare tilde",
			hooksPath: "~",
			wantDir:   fakeHome,
		},
		{
			name:      "relative path without tilde",
			hooksPath: ".beads/hooks",
			wantDir:   "REPO_RELATIVE:.beads/hooks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest gets its own repo to avoid git config corruption.
			// Setting core.hooksPath to a backslash-tilde path (e.g. ~\.githooks)
			// causes all subsequent git commands to fail with "failed to expand
			// user dir", and even `git config --unset` cannot recover.
			//
			// IMPORTANT: setupTestRepo must run BEFORE overriding HOME, because
			// git init/commit need the real HOME for global config access
			// (e.g. safe.directory on CI). Overriding HOME too early causes
			// git config to fail with exit status 128 on some environments.
			subRepoPath, subCleanup := setupTestRepo(t)
			defer subCleanup()

			// Override HOME after repo setup so tilde expansion resolves
			// to fakeHome deterministically for the code under test.
			origHome := os.Getenv("HOME")
			os.Setenv("HOME", fakeHome)
			t.Cleanup(func() {
				if origHome != "" {
					os.Setenv("HOME", origHome)
				} else {
					os.Unsetenv("HOME")
				}
			})

			ResetCaches()

			cmd := exec.Command("git", "config", "core.hooksPath", tt.hooksPath)
			cmd.Dir = subRepoPath
			if err := cmd.Run(); err != nil {
				t.Skipf("git config rejected core.hooksPath %q: %v", tt.hooksPath, err)
			}

			originalDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			if err := os.Chdir(subRepoPath); err != nil {
				t.Fatalf("Failed to chdir to test repo: %v", err)
			}
			t.Cleanup(func() { os.Chdir(originalDir) })

			gotDir, err := GetGitHooksDir()
			if err != nil {
				t.Fatalf("GetGitHooksDir() returned error: %v", err)
			}

			wantDir := tt.wantDir
			const repoRelPrefix = "REPO_RELATIVE:"
			if len(wantDir) > len(repoRelPrefix) && wantDir[:len(repoRelPrefix)] == repoRelPrefix {
				wantDir = filepath.Join(subRepoPath, wantDir[len(repoRelPrefix):])
			}

			// On macOS, /var is a symlink to /private/var, so we need to resolve
			// symlinks before comparing paths for equality.
			gotDirResolved, _ := filepath.EvalSymlinks(gotDir)
			wantDirResolved, _ := filepath.EvalSymlinks(wantDir)
			if gotDirResolved != wantDirResolved {
				t.Errorf("GetGitHooksDir() = %q (resolved: %q), want %q (resolved: %q)",
					gotDir, gotDirResolved, wantDir, wantDirResolved)
			}
		})
	}
}

func TestIsMainRepoBare(t *testing.T) {
	t.Run("returns false for regular repo", func(t *testing.T) {
		tmpDir := t.TempDir()
		repoPath := filepath.Join(tmpDir, "regular-repo")
		if err := os.MkdirAll(repoPath, 0750); err != nil {
			t.Fatalf("Failed to create test repo directory: %v", err)
		}
		cmd := exec.Command("git", "init")
		cmd.Dir = repoPath
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Failed to init git repo: %v\nOutput: %s", err, string(output))
		}

		ResetCaches()
		origDir, _ := os.Getwd()
		defer os.Chdir(origDir)
		os.Chdir(repoPath)

		if IsMainRepoBare() {
			t.Error("Expected IsMainRepoBare() to return false for regular repo")
		}
	})

	t.Run("returns true for worktree in bare repo pattern", func(t *testing.T) {
		tmpDir := t.TempDir()
		projectDir := filepath.Join(tmpDir, "project")

		runGit := func(dir string, args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
			}
		}

		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatalf("Failed to create project dir: %v", err)
		}

		runGit(projectDir, "init", "--bare", ".bare")

		gitFile := filepath.Join(projectDir, ".git")
		if err := os.WriteFile(gitFile, []byte("gitdir: .bare"), 0644); err != nil {
			t.Fatalf("Failed to create .git file: %v", err)
		}

		runGit(projectDir, "config", "user.email", "test@test.com")
		runGit(projectDir, "config", "user.name", "Test")

		runGit(projectDir, "worktree", "add", "main", "-b", "main")

		ResetCaches()
		origDir, _ := os.Getwd()
		defer os.Chdir(origDir)
		os.Chdir(filepath.Join(projectDir, "main"))

		if !IsWorktree() {
			t.Error("Expected IsWorktree() to return true for worktree in bare repo pattern")
		}
		if !IsMainRepoBare() {
			t.Error("Expected IsMainRepoBare() to return true for worktree in bare repo pattern")
		}
	})
}
