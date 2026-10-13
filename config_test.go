package main

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestConfigRoundTripAndToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := loadConfig()
	if len(cfg.Hidden) != 0 {
		t.Fatalf("expected empty config, got %+v", cfg)
	}

	if !cfg.toggleHidden("/g/backend") {
		t.Fatal("first toggle should hide")
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "arbr", "config.json")); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	cfg = loadConfig()
	if !cfg.isHidden("/g/backend") {
		t.Fatal("hidden entry not persisted")
	}
	if cfg.toggleHidden("/g/backend") {
		t.Fatal("second toggle should unhide")
	}
	if cfg.isHidden("/g/backend") {
		t.Fatal("unhide did not remove entry")
	}
}

func TestDefaultBranchFor(t *testing.T) {
	cfg := config{DefaultBranch: map[string]string{
		"/g/a":     "develop",
		"~/proj/x": "trunk",
	}}
	if got := cfg.defaultBranchFor("/g/a"); got != "develop" {
		t.Errorf("absolute key: got %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := cfg.defaultBranchFor(filepath.Join(home, "proj", "x")); got != "trunk" {
		t.Errorf("~ key not expanded: got %q", got)
	}
	if got := cfg.defaultBranchFor("/unconfigured"); got != "" {
		t.Errorf("unconfigured project should return empty, got %q", got)
	}
}

func TestSyncFilesFor(t *testing.T) {
	cfg := config{SyncFiles: map[string][]string{
		"/g/a":     {".env"},
		"~/proj/x": {".npmrc", "apps/web/.env.local"},
	}}
	if got := cfg.syncFilesFor("/g/a"); len(got) != 1 || got[0] != ".env" {
		t.Errorf("absolute key: got %v", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := cfg.syncFilesFor(filepath.Join(home, "proj", "x")); len(got) != 2 {
		t.Errorf("~ key not expanded: got %v", got)
	}
	if got := cfg.syncFilesFor("/unconfigured"); got != nil {
		t.Errorf("unconfigured project should return nil, got %v", got)
	}
}

func TestUndoHide(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := newApp("/g", "")
	m.width, m.height = 100, 30
	m.allProjects = []project{
		{name: "a", path: "/g/a"},
		{name: "b", path: "/g/b"},
	}
	m.applyFilter()

	// hide b, then undo
	m.projCursor = 1
	next, _ := m.handleProjectsKey(keyMsg("x"))
	m = next.(appModel)
	if len(m.projects) != 1 || !m.cfg.isHidden("/g/b") {
		t.Fatalf("hide failed: %+v", m.projects)
	}
	next, _ = m.handleProjectsKey(keyMsg("u"))
	m = next.(appModel)
	if len(m.projects) != 2 || m.cfg.isHidden("/g/b") {
		t.Fatalf("undo failed: %+v hidden=%v", m.projects, m.cfg.Hidden)
	}
	if m.projects[m.projCursor].path != "/g/b" {
		t.Fatalf("cursor should sit on restored project, got %+v", m.projects[m.projCursor])
	}

	// with the history exhausted, u is a no-op
	next, _ = m.handleProjectsKey(keyMsg("u"))
	m = next.(appModel)
	if m.status != "nothing to undo" {
		t.Fatalf("want 'nothing to undo', got %q", m.status)
	}
	if len(m.projects) != 2 || len(m.cfg.Hidden) != 0 {
		t.Fatalf("undo with empty history must not change state: %+v", m.cfg.Hidden)
	}
}

func TestHiddenProjectFilter(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := newApp("/g", "")
	m.width, m.height = 100, 30
	m.allProjects = []project{
		{name: "a", path: "/g/a"},
		{name: "b", path: "/g/b"},
		{name: "c", path: "/g/c"},
	}
	m.cfg.toggleHidden("/g/b")
	m.applyFilter()

	if len(m.projects) != 2 || m.projects[0].name != "a" || m.projects[1].name != "c" {
		t.Fatalf("hidden project not filtered: %+v", m.projects)
	}
	if m.hiddenCount() != 1 {
		t.Fatalf("hiddenCount = %d, want 1", m.hiddenCount())
	}

	m.showHidden = true
	m.applyFilter()
	if len(m.projects) != 3 || !m.projects[1].hidden {
		t.Fatalf("show-hidden view wrong: %+v", m.projects)
	}

	// cursor clamps when the filtered list shrinks
	m.projCursor = 2
	m.showHidden = false
	m.applyFilter()
	if m.projCursor != 1 {
		t.Fatalf("cursor not clamped: %d", m.projCursor)
	}
}
