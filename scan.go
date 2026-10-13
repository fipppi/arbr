package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const worktreeDirSuffix = "__worktrees"

type project struct {
	name      string
	path      string
	worktrees int
	recent    []string // linked worktree names, most recently modified first
	hidden    bool
}

// scanProjects lists git repositories directly under root, skipping hidden
// directories and *__worktrees containers.
func scanProjects(root string) ([]project, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var projects []project
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), worktreeDirSuffix) {
			continue
		}
		path := filepath.Join(root, e.Name())
		fi, err := os.Stat(filepath.Join(path, ".git"))
		if err != nil || !fi.IsDir() {
			continue
		}
		p := project{name: e.Name(), path: path}
		p.worktrees, p.recent = scanWorktreeNames(path)
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].name) < strings.ToLower(projects[j].name)
	})
	return projects, nil
}

// scanWorktreeNames returns the linked worktree count and names for a repo,
// most recently modified first.
func scanWorktreeNames(repo string) (int, []string) {
	adminRoot := filepath.Join(repo, ".git", "worktrees")
	admin, err := os.ReadDir(adminRoot)
	if err != nil {
		return 0, nil
	}
	type wtEntry struct {
		name string
		mod  time.Time
	}
	var es []wtEntry
	for _, a := range admin {
		if !a.IsDir() {
			continue
		}
		dir := filepath.Join(adminRoot, a.Name())
		es = append(es, wtEntry{a.Name(), latestMtime(filepath.Join(dir, "index"), filepath.Join(dir, "HEAD"))})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].mod.After(es[j].mod) })
	names := make([]string, 0, len(es))
	for _, e := range es {
		names = append(names, e.name)
	}
	return len(names), names
}

type worktree struct {
	name     string
	path     string
	branch   string
	isMain   bool
	missing  bool
	locked   bool
	pinned   bool // from arbr config; protected from prune and sweep
	created  time.Time
	modified time.Time
}

// listWorktrees returns the main worktree followed by every entry of
// <repo>/.git/worktrees/.
func listWorktrees(repo string) ([]worktree, error) {
	gitDir := filepath.Join(repo, ".git")
	main := worktree{
		name:     filepath.Base(repo),
		path:     repo,
		isMain:   true,
		branch:   readBranch(filepath.Join(gitDir, "HEAD")),
		created:  birthtime(repo),
		modified: latestMtime(filepath.Join(gitDir, "index"), filepath.Join(gitDir, "HEAD")),
	}
	wts := []worktree{main}

	adminRoot := filepath.Join(gitDir, "worktrees")
	entries, err := os.ReadDir(adminRoot)
	if err != nil {
		return wts, nil // repo has no linked worktrees
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		admin := filepath.Join(adminRoot, e.Name())
		wt := worktree{
			name:     e.Name(),
			branch:   readBranch(filepath.Join(admin, "HEAD")),
			created:  birthtime(admin),
			modified: latestMtime(filepath.Join(admin, "index"), filepath.Join(admin, "HEAD")),
		}
		if b, err := os.ReadFile(filepath.Join(admin, "gitdir")); err == nil {
			// gitdir points at <worktree>/.git
			wt.path = filepath.Dir(strings.TrimSpace(string(b)))
		}
		if _, err := os.Stat(filepath.Join(admin, "locked")); err == nil {
			wt.locked = true
		}
		if wt.path == "" {
			wt.missing = true
		} else if _, err := os.Stat(wt.path); err != nil {
			wt.missing = true
		}
		wts = append(wts, wt)
	}
	return wts, nil
}

func readBranch(headPath string) string {
	b, err := os.ReadFile(headPath)
	if err != nil {
		return "?"
	}
	s := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(s, "ref: refs/heads/"); ok {
		return ref
	}
	if len(s) >= 7 {
		return "detached " + s[:7]
	}
	return s
}

func latestMtime(paths ...string) time.Time {
	var latest time.Time
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(latest) {
			latest = fi.ModTime()
		}
	}
	return latest
}

// ageStyle colours a timestamp from green (today) fading to grey after two
// weeks of inactivity.
func ageStyle(t time.Time) lipgloss.Style {
	if t.IsZero() {
		return subtleStyle
	}
	d := time.Since(t)
	for i, step := range []time.Duration{
		24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour,
	} {
		if d < step {
			return ageRamp[i]
		}
	}
	return subtleStyle
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}
