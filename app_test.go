package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestNameColorDeterministic(t *testing.T) {
	for _, name := range []string{"backend", "easydev", "arbr"} {
		if nameColor(name) != nameColor(name) {
			t.Errorf("nameColor(%q) not deterministic", name)
		}
	}
	// different names should generally land on different palette entries
	if nameColor("backend") == nameColor("easydev") && nameColor("backend") == nameColor("arbr") {
		t.Error("palette mapping looks degenerate: three names share one colour")
	}
}

func TestFuzzyFilter(t *testing.T) {
	if _, ok := fuzzyScore("backend", "bke"); !ok {
		t.Error("bke should fuzzy-match backend")
	}
	if _, ok := fuzzyScore("backend", "xyz"); ok {
		t.Error("xyz should not match backend")
	}
	if _, ok := fuzzyScore("backend", "BACK"); !ok {
		t.Error("match should be case-insensitive")
	}
	exact, _ := fuzzyScore("backend", "back")
	sparse, _ := fuzzyScore("bluewhackend", "back")
	if exact <= sparse {
		t.Errorf("consecutive prefix match should outscore sparse match: %d vs %d", exact, sparse)
	}

	m := newApp("/tmp/root", "")
	m.allProjects = []project{
		{name: "backend", path: "/p/backend"},
		{name: "easydev", path: "/p/easydev"},
		{name: "broad-end", path: "/p/broad-end"},
	}
	m.filter = "bkd"
	m.applyFilter()
	if len(m.projects) != 1 || m.projects[0].name != "backend" {
		t.Errorf("filter bkd: want [backend], got %+v", m.projects)
	}
	m.filter = ""
	m.applyFilter()
	if len(m.projects) != 3 {
		t.Errorf("clearing filter should restore all projects, got %+v", m.projects)
	}
}

func TestStaleWorktrees(t *testing.T) {
	now := time.Now()
	old := now.Add(-45 * 24 * time.Hour)
	wts := []worktree{
		{name: "main", isMain: true, modified: old},                   // main: never
		{name: "fresh", modified: now.Add(-time.Hour)},                // active: no
		{name: "stale", modified: old},                                // yes
		{name: "pinned", pinned: true, modified: old},                 // pinned: no
		{name: "locked", locked: true, modified: old},                 // locked: no
		{name: "gone", missing: true, modified: old},                  // missing: no
		{name: "unknown"},                                             // zero time: no
		{name: "borderline", modified: now.Add(-29 * 24 * time.Hour)}, // < 1 month: no
	}
	got := staleWorktrees(wts, sweepCutoff)
	if len(got) != 1 || got[0].name != "stale" {
		t.Fatalf("staleWorktrees = %+v, want [stale]", got)
	}
}

func TestPinnedProtection(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := newApp("/g", "")
	m.width, m.height = 100, 40
	m.view = viewWorktrees
	m.repo, m.repoName = "/g/backend", "backend"
	m.wts = []worktree{
		{name: "backend", path: "/g/backend", isMain: true, modified: time.Now()},
		{name: "keep", branch: "keep", path: "/w/keep", modified: time.Now()},
	}

	// * pins the selected worktree and persists it
	m.wtCursor = 1
	next, _ := m.handleKey(keyMsg("*"))
	m = next.(appModel)
	if !m.wts[1].pinned || !m.cfg.isPinned("/w/keep") {
		t.Fatalf("pin failed: %+v", m.wts[1])
	}
	if !strings.Contains(m.View(), "pinned") {
		t.Error("pinned tag missing from worktree row")
	}

	// d refuses to touch a pinned worktree
	next, _ = m.handleKey(keyMsg("d"))
	m = next.(appModel)
	if m.mode != modeNormal || !strings.Contains(m.status, "pinned") {
		t.Fatalf("d should be blocked on pinned worktree: %q", m.status)
	}

	// prune withholds pinned entries and queues them for lock-protection
	m.wts = append(m.wts, worktree{name: "gone", branch: "gone", path: "/w/gone", missing: true, pinned: true})
	next, _ = m.Update(pruneListMsg{names: []string{"gone", "dead"}})
	m = next.(appModel)
	if len(m.pruneList) != 1 || m.pruneList[0] != "dead" {
		t.Fatalf("pinned entry should be withheld from prune: %v", m.pruneList)
	}
	if len(m.pruneProtect) != 1 || m.pruneProtect[0] != "/w/gone" {
		t.Fatalf("pinned entry should be lock-protected: %v", m.pruneProtect)
	}

	// when everything prunable is pinned, no prompt appears
	m.mode = modeNormal
	next, _ = m.Update(pruneListMsg{names: []string{"gone"}})
	m = next.(appModel)
	if m.mode != modeNormal || m.status != "nothing to prune (1 pinned)" {
		t.Fatalf("all-pinned prune should be a no-op: mode=%v status=%q", m.mode, m.status)
	}

	// * again unpins
	m.wtCursor = 1
	next, _ = m.handleKey(keyMsg("*"))
	m = next.(appModel)
	if m.wts[1].pinned || m.cfg.isPinned("/w/keep") {
		t.Fatal("unpin failed")
	}
}

func TestSweepStateMachine(t *testing.T) {
	m := newApp("/g", "")
	m.width, m.height = 100, 40
	m.view = viewWorktrees
	m.repo, m.repoName = "/g/backend", "backend"
	old := time.Now().Add(-60 * 24 * time.Hour)
	m.wts = []worktree{
		{name: "backend", path: "/g/backend", isMain: true, modified: time.Now()},
		{name: "a", branch: "a", path: "/w/a", modified: old},
		{name: "b", branch: "b", path: "/w/b", modified: old},
	}

	// P collects the stale pair and asks for confirmation
	next, _ := m.handleKey(keyMsg("P"))
	m = next.(appModel)
	if m.mode != modeConfirmSweep || len(m.sweepItems) != 2 {
		t.Fatalf("P should queue 2 items, got mode=%v items=%d", m.mode, len(m.sweepItems))
	}

	// y starts the job: first item active (red + spinner), 1-by-1
	next, cmd := m.handleKey(keyMsg("y"))
	m = next.(appModel)
	if m.mode != modeSweeping || m.sweepItems[0].state != sweepActive || cmd == nil {
		t.Fatalf("y should start sweeping item 0: %+v", m.sweepItems)
	}
	if out := m.View(); !strings.Contains(out, "deleting…") || !strings.Contains(out, "removing stale worktrees… 1/2") {
		t.Errorf("progress view missing active state\n%s", out)
	}

	// item 0 done → item 1 becomes active
	next, cmd = m.Update(sweepDoneMsg{0, nil})
	m = next.(appModel)
	if m.sweepItems[0].state != sweepRemoved || m.sweepItems[1].state != sweepActive || cmd == nil {
		t.Fatalf("after first done: %+v", m.sweepItems)
	}
	if out := m.View(); !strings.Contains(out, "removed") {
		t.Errorf("progress view should show completed item\n%s", out)
	}

	// item 1 fails → job ends with a summary
	next, _ = m.Update(sweepDoneMsg{1, errDirty{}})
	m = next.(appModel)
	if m.mode != modeNormal || m.sweepItems != nil {
		t.Fatalf("sweep should finish: mode=%v", m.mode)
	}
	if m.status != "removed 1, skipped 1 (dirty or locked)" || !m.statusErr {
		t.Fatalf("summary wrong: %q", m.status)
	}
}

type errDirty struct{}

func (errDirty) Error() string { return "contains modified or untracked files" }

func TestFooterNotGluedToScrolledList(t *testing.T) {
	m := newApp("/tmp/root", "")
	m.width, m.height = 80, 12
	m.view = viewWorktrees
	m.repo, m.repoName = "/tmp/root/backend", "backend"
	for i := 0; i < 20; i++ {
		m.wts = append(m.wts, worktree{
			name: "wt", branch: "branch", path: "/tmp/root/backend__worktrees/wt",
			created: time.Now(), modified: time.Now(),
		})
	}
	m.wts[0].isMain = true
	m.status = "nothing to prune"

	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "nothing to prune") && strings.Contains(line, "/20") {
			t.Fatalf("status glued to page indicator: %q", line)
		}
	}
}

func TestShortenPaths(t *testing.T) {
	long := "/data/git/backend__worktrees/feat-x"
	short := "/data/git/repo"

	// everything fits: nothing is collapsed
	got := shortenPaths([]string{long, short}, 80)
	if got[0] != long || got[1] != short {
		t.Errorf("wide: got %v", got)
	}

	// one path too long: ALL paths collapse, parent and leaf stay whole
	got = shortenPaths([]string{long, short}, 30)
	if got[0] != "/d/g/backend__worktrees/feat-x" {
		t.Errorf("long path: got %q", got[0])
	}
	if got[1] != "/d/git/repo" {
		t.Errorf("short path must collapse too (parent 'git' stays whole): got %q", got[1])
	}

	// still-too-long collapsed paths are …-truncated individually
	got = shortenPaths([]string{long, short}, 14)
	if got[0] != "/d/g/backend_…" {
		t.Errorf("truncate: got %q", got[0])
	}
	if got[1] != "/d/git/repo" {
		t.Errorf("fitting path should not be truncated: got %q", got[1])
	}
}

func TestAgeStyleRamp(t *testing.T) {
	now := time.Now()
	cases := []struct {
		age  time.Duration
		want lipgloss.Style
	}{
		{time.Hour, ageRamp[0]},
		{2 * 24 * time.Hour, ageRamp[1]},
		{5 * 24 * time.Hour, ageRamp[2]},
		{10 * 24 * time.Hour, ageRamp[3]},
		{20 * 24 * time.Hour, subtleStyle},
	}
	for _, c := range cases {
		got := ageStyle(now.Add(-c.age))
		if got.GetForeground() != c.want.GetForeground() {
			t.Errorf("ageStyle(now-%v) = %v, want %v", c.age, got.GetForeground(), c.want.GetForeground())
		}
	}
	if ageStyle(time.Time{}).GetForeground() != subtleStyle.GetForeground() {
		t.Error("zero time should render grey")
	}
}

func TestCurrentProjectPinned(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := newApp("/g", "/g/b")
	m.width, m.height = 100, 40
	m.allProjects = []project{
		{name: "a", path: "/g/a"},
		{name: "b", path: "/g/b"},
		{name: "c", path: "/g/c"},
	}
	m.applyFilter()
	if m.projects[0].name != "b" {
		t.Fatalf("current project not pinned first: %+v", m.projects)
	}
	if out := m.View(); !strings.Contains(out, "●") {
		t.Errorf("current project marker ● missing\n%s", out)
	}

	// a hidden current project stays visible (and pinned)
	m.cfg.toggleHidden("/g/b")
	m.applyFilter()
	if len(m.projects) != 3 || m.projects[0].name != "b" || !m.projects[0].hidden {
		t.Fatalf("hidden current project should stay pinned: %+v", m.projects)
	}

	// a repo outside the scan root is synthesized into the list
	m2 := newApp("/g", "/elsewhere/repo")
	m2.allProjects = []project{{name: "a", path: "/g/a"}}
	m2.resolveCurrent()
	if len(m2.allProjects) != 2 || m2.allProjects[0].path != "/elsewhere/repo" {
		t.Fatalf("out-of-root current repo not synthesized: %+v", m2.allProjects)
	}
}

func TestGridAndListRender(t *testing.T) {
	m := newApp("/tmp/root", "")
	m.width, m.height = 100, 40
	m.projects = []project{
		{name: "backend", path: "/tmp/root/backend", worktrees: 47,
			recent: []string{"cas-3760-e2e", "cas-1208-frontend", "cas-1999-something", "cas-1212-test", "cas-0001-old"}},
		{name: "easydev", path: "/tmp/root/easydev"},
		{name: "arbr", path: "/tmp/root/arbr", worktrees: 1, recent: []string{"feat-x"}},
	}
	out := m.View()
	for _, want := range []string{"arbr", "backend", "easydev", "no worktrees", "enter",
		"cas-3760-e2e", "cas-1999-something", "… 44 more", "feat-x", "filter", "────"} {
		if !strings.Contains(out, want) {
			t.Errorf("project list missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "cas-1212-test") {
		t.Errorf("preview should cap at %d worktrees\n%s", previewMax, out)
	}

	// an applied filter renders as a bar above the list
	m.filter = "back"
	if out := m.View(); !strings.Contains(out, "filter back") {
		t.Errorf("filter bar missing from top of view\n%s", out)
	}
	m.filter = ""

	m.view = viewWorktrees
	m.repo, m.repoName = "/tmp/root/backend", "backend"
	m.wts = []worktree{
		{name: "backend", path: "/tmp/root/backend", branch: "master", isMain: true,
			created: time.Now().Add(-90 * 24 * time.Hour), modified: time.Now().Add(-time.Hour)},
		{name: "cas-123", path: "/tmp/root/backend__worktrees/cas-123", branch: "cas-123",
			created: time.Now().Add(-48 * time.Hour), modified: time.Now().Add(-10 * time.Minute)},
		{name: "gone", branch: "gone", missing: true,
			created: time.Now().Add(-200 * 24 * time.Hour), modified: time.Now().Add(-30 * 24 * time.Hour)},
	}
	m.sortWts()
	out = m.View()
	for _, want := range []string{"backend", "cas-123", "main", "missing", "BRANCH", "CREATED", "MODIFIED",
		"PATH", "/tmp/root/backend__worktrees/cas-123", "2d ago", "10m ago", "prune"} {
		if !strings.Contains(out, want) {
			t.Errorf("worktree view missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "NAME") {
		t.Errorf("NAME column should be gone\n%s", out)
	}

	// the prune confirmation lists what would be removed
	m.mode = modeConfirmPrune
	m.pruneList = []string{"gone"}
	out = m.View()
	for _, want := range []string{"prune 1 stale worktree entry? (y/N)", "gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("prune confirmation missing %q\n%s", want, out)
		}
	}
	m.mode = modeNormal
	m.pruneList = nil

	// sort toggle: created-desc puts cas-123 (2d) before gone (200d); main stays pinned
	m.sortBy = sortCreated
	m.sortWts()
	if m.wts[0].name != "backend" || m.wts[1].name != "cas-123" {
		t.Errorf("sort order wrong: %s, %s", m.wts[0].name, m.wts[1].name)
	}
	m.sortBy = sortModified
	m.sortWts()
	if m.wts[1].name != "cas-123" || m.wts[2].name != "gone" {
		t.Errorf("modified sort wrong: %s, %s", m.wts[1].name, m.wts[2].name)
	}
}
