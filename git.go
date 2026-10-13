package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func git(repo string, args ...string) error {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// createWorktree adds a worktree at <root>/<project>__worktrees/<name>,
// reusing the branch <name> if it exists. Otherwise the new branch starts
// from base, or from the repo's default branch when base is empty. After
// creation, syncFiles (repo-relative, typically gitignored .env files) are
// copied over from the main checkout.
func createWorktree(repo, name, base string, syncFiles []string) error {
	dest := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+worktreeDirSuffix, name)
	var err error
	if branchExists(repo, name) {
		err = git(repo, "worktree", "add", dest, name)
	} else {
		if base == "" {
			base = defaultBranch(repo)
		}
		err = git(repo, "worktree", "add", dest, "-b", name, base)
	}
	if err != nil {
		return err
	}
	return copySyncFiles(repo, dest, syncFiles)
}

// copySyncFiles copies per-project config files from the main checkout into
// a new worktree. Missing sources are skipped; existing targets are never
// overwritten.
func copySyncFiles(repo, dest string, files []string) error {
	var failed []string
	for _, f := range files {
		src := filepath.Join(repo, f)
		dst := filepath.Join(dest, f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := copyFile(src, dst); err != nil {
			failed = append(failed, f)
		}
	}
	if len(failed) > 0 {
		return errors.New("worktree created, but failed to sync " + strings.Join(failed, ", "))
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src) // sync files are small configs
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(src); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, mode)
}

func branchExists(repo, name string) bool {
	return exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+name).Run() == nil
}

// defaultBranch resolves the repo's default branch: origin/HEAD if set,
// then a local main/master, falling back to the current HEAD.
func defaultBranch(repo string) string {
	if out, err := exec.Command("git", "-C", repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		s := strings.TrimSpace(string(out))
		if _, b, ok := strings.Cut(s, "/"); ok && b != "" {
			return b
		}
	}
	for _, b := range []string{"main", "master"} {
		if branchExists(repo, b) {
			return b
		}
	}
	return "HEAD"
}

func removeWorktree(repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return git(repo, append(args, path)...)
}

func pruneWorktrees(repo string) error {
	return git(repo, "worktree", "prune")
}

// pruneWorktreesProtecting prunes stale entries while temporarily locking
// the given worktree paths, so pinned worktrees survive even when their
// directory is missing. Worktrees that were already locked stay locked.
func pruneWorktreesProtecting(repo string, protect []string) error {
	var locked []string
	for _, p := range protect {
		if git(repo, "worktree", "lock", "--reason", "arbr pin", p) == nil {
			locked = append(locked, p)
		}
	}
	err := pruneWorktrees(repo)
	for _, p := range locked {
		_ = git(repo, "worktree", "unlock", p)
	}
	return err
}

// prunableWorktrees returns the admin ids that `git worktree prune` would
// remove, via a dry run.
func prunableWorktrees(repo string) ([]string, error) {
	out, err := exec.Command("git", "-C", repo, "worktree", "prune", "--dry-run", "--verbose").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Removing worktrees/"); ok {
			name, _, _ := strings.Cut(rest, ":")
			names = append(names, name)
		}
	}
	return names, nil
}
