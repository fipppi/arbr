package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func setupRepo(t *testing.T) (root, repo string) {
	t.Helper()
	root = t.TempDir()
	repo = filepath.Join(root, "demo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "git", "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "git", "add", ".")
	mustRun(t, repo, "git", "commit", "-m", "init")
	return root, repo
}

func TestScanProjectsSkipsWorktreeDirs(t *testing.T) {
	root, _ := setupRepo(t)
	for _, d := range []string{"demo__worktrees", "not-a-repo", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := scanProjects(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].name != "demo" {
		t.Fatalf("want [demo], got %+v", ps)
	}
}

func TestCreateWorktreeBaseBranch(t *testing.T) {
	_, repo := setupRepo(t)
	tip := func(ref string) string {
		out, err := exec.Command("git", "-C", repo, "rev-parse", ref).Output()
		if err != nil {
			t.Fatalf("rev-parse %s: %v", ref, err)
		}
		return strings.TrimSpace(string(out))
	}

	// develop stays at the first commit; main moves ahead and is checked out
	mustRun(t, repo, "git", "branch", "develop")
	mustRun(t, repo, "git", "commit", "--allow-empty", "-m", "ahead")

	// explicit base (per-project config override) wins over HEAD
	if err := createWorktree(repo, "feat-dev", "develop", nil); err != nil {
		t.Fatal(err)
	}
	if tip("feat-dev") != tip("develop") || tip("feat-dev") == tip("main") {
		t.Fatalf("feat-dev should start from develop: %s dev=%s main=%s",
			tip("feat-dev"), tip("develop"), tip("main"))
	}

	// empty base falls back to the repo default branch (main here)
	if err := createWorktree(repo, "feat-main", "", nil); err != nil {
		t.Fatal(err)
	}
	if tip("feat-main") != tip("main") {
		t.Fatalf("feat-main should start from main")
	}
	if db := defaultBranch(repo); db != "main" {
		t.Fatalf("defaultBranch = %q, want main", db)
	}
}

func TestCreateWorktreeSyncsFiles(t *testing.T) {
	root, repo := setupRepo(t)

	// gitignored local files in the main checkout
	for f, content := range map[string]string{
		".gitignore":          ".env\n.env.local\n",
		".env":                "SECRET=1\n",
		"apps/web/.env.local": "LOCAL=1\n",
	} {
		p := filepath.Join(repo, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, repo, "git", "add", ".gitignore")
	mustRun(t, repo, "git", "commit", "-m", "ignore env")

	sync := []string{".env", "apps/web/.env.local", "does-not-exist.env"}
	if err := createWorktree(repo, "feat-sync", "", sync); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(root, "demo__worktrees", "feat-sync")
	for f, want := range map[string]string{
		".env":                "SECRET=1\n",
		"apps/web/.env.local": "LOCAL=1\n",
	} {
		b, err := os.ReadFile(filepath.Join(wt, f))
		if err != nil {
			t.Fatalf("%s not synced: %v", f, err)
		}
		if string(b) != want {
			t.Fatalf("%s content = %q, want %q", f, b, want)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, "does-not-exist.env")); err == nil {
		t.Fatal("missing source should be skipped, not created")
	}
}

func TestPruneProtectsPinned(t *testing.T) {
	root, repo := setupRepo(t)
	if err := createWorktree(repo, "pin-me", "", nil); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(root, "demo__worktrees", "pin-me")
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}

	// a protected prune keeps the pinned entry alive
	if err := pruneWorktreesProtecting(repo, []string{wtPath}); err != nil {
		t.Fatal(err)
	}
	wts, _ := listWorktrees(repo)
	if len(wts) != 2 {
		t.Fatalf("pinned worktree was pruned: %+v", wts)
	}

	// the temporary lock is released, so an unprotected prune can take it
	if err := pruneWorktrees(repo); err != nil {
		t.Fatal(err)
	}
	wts, _ = listWorktrees(repo)
	if len(wts) != 1 {
		t.Fatalf("unpinned prune should clear it: %+v", wts)
	}
}

func TestListCreateRemoveWorktrees(t *testing.T) {
	root, repo := setupRepo(t)

	if err := createWorktree(repo, "feature-x", "", nil); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(root, "demo__worktrees", "feature-x")
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree dir not created: %v", err)
	}

	wts, err := listWorktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("want 2 worktrees, got %d: %+v", len(wts), wts)
	}
	if !wts[0].isMain || wts[0].branch != "main" {
		t.Fatalf("main worktree wrong: %+v", wts[0])
	}
	wt := wts[1]
	wantPath, _ := filepath.EvalSymlinks(wtPath)
	gotPath, _ := filepath.EvalSymlinks(wt.path)
	if wt.name != "feature-x" || wt.branch != "feature-x" || gotPath != wantPath {
		t.Fatalf("linked worktree wrong: %+v", wt)
	}
	if wt.created.IsZero() || wt.modified.IsZero() {
		t.Fatalf("timestamps missing: %+v", wt)
	}

	// missing dir is flagged, prune clears it
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}
	wts, _ = listWorktrees(repo)
	if len(wts) != 2 || !wts[1].missing {
		t.Fatalf("want missing flag, got %+v", wts)
	}
	prunable, err := prunableWorktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(prunable) != 1 || prunable[0] != "feature-x" {
		t.Fatalf("prunable = %v, want [feature-x]", prunable)
	}
	if err := pruneWorktrees(repo); err != nil {
		t.Fatal(err)
	}
	wts, _ = listWorktrees(repo)
	if len(wts) != 1 {
		t.Fatalf("prune did not clear worktree: %+v", wts)
	}

	// create on an existing branch reuses it
	if err := createWorktree(repo, "feature-x", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(repo, filepath.Join(root, "demo__worktrees", "feature-x"), false); err != nil {
		t.Fatal(err)
	}
	wts, _ = listWorktrees(repo)
	if len(wts) != 1 {
		t.Fatalf("remove failed: %+v", wts)
	}
}
