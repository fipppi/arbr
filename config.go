package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type config struct {
	Hidden []string `json:"hidden"`
	// DefaultBranch maps a project path (absolute, or ~-prefixed) to the
	// branch new worktrees should start from, overriding the repo default.
	DefaultBranch map[string]string `json:"default_branch,omitempty"`
	// SyncFiles maps a project path (absolute, or ~-prefixed) to
	// repo-relative files (typically gitignored .env files) copied from the
	// main checkout into every new worktree.
	SyncFiles map[string][]string `json:"sync_files,omitempty"`
	// Pinned worktree paths are protected from prune and sweep-stale.
	Pinned []string `json:"pinned,omitempty"`
}

func configPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "arbr", "config.json")
}

func loadConfig() config {
	var cfg config
	b, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(b, &cfg)
	return cfg
}

func saveConfig(cfg config) error {
	p := configPath()
	if p == "" {
		return errors.New("cannot resolve config path")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// defaultBranchFor returns the configured base branch for a project, if any.
func (c config) defaultBranchFor(path string) string {
	if b, ok := c.DefaultBranch[path]; ok {
		return b
	}
	for k, v := range c.DefaultBranch {
		if expandHome(k) == path {
			return v
		}
	}
	return ""
}

// syncFilesFor returns the files to copy into new worktrees of a project.
func (c config) syncFilesFor(path string) []string {
	if fs, ok := c.SyncFiles[path]; ok {
		return fs
	}
	for k, v := range c.SyncFiles {
		if expandHome(k) == path {
			return v
		}
	}
	return nil
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

func (c config) isHidden(path string) bool {
	return slices.Contains(c.Hidden, path)
}

// toggleHidden flips a project's hidden state and reports the new state.
func (c *config) toggleHidden(path string) bool {
	if i := slices.Index(c.Hidden, path); i >= 0 {
		c.Hidden = slices.Delete(c.Hidden, i, i+1)
		return false
	}
	c.Hidden = append(c.Hidden, path)
	slices.Sort(c.Hidden)
	return true
}

func (c config) isPinned(path string) bool {
	return slices.Contains(c.Pinned, path)
}

// togglePinned flips a worktree's pinned state and reports the new state.
func (c *config) togglePinned(path string) bool {
	if i := slices.Index(c.Pinned, path); i >= 0 {
		c.Pinned = slices.Delete(c.Pinned, i, i+1)
		return false
	}
	c.Pinned = append(c.Pinned, path)
	slices.Sort(c.Pinned)
	return true
}
