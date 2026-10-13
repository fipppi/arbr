package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type view int

const (
	viewProjects view = iota
	viewWorktrees
)

type mode int

const (
	modeNormal mode = iota
	modeCreate
	modeConfirmDelete
	modeConfirmForce
	modeConfirmPrune
	modeConfirmSweep
	modeSweeping
	modeFilter
)

// sweepState tracks one stale worktree through the 1-by-1 cleanup job.
type sweepState int

const (
	sweepPending sweepState = iota
	sweepActive
	sweepRemoved
	sweepFailed
)

type sweepItem struct {
	wt     worktree
	state  sweepState
	errMsg string
}

type sortMode int

const (
	sortModified sortMode = iota
	sortCreated
)

func (s sortMode) String() string {
	if s == sortCreated {
		return "created"
	}
	return "modified"
}

// previewMax is how many recent worktree names a project row previews.
const previewMax = 3

// pruneShowMax caps how many prunable worktrees the confirmation lists.
const pruneShowMax = 6

// sweepCutoff is how long a worktree can go untouched before P offers to
// remove it.
const sweepCutoff = 30 * 24 * time.Hour

// staleWorktrees lists removable worktrees with no activity since cutoff.
// The main worktree, locked or missing entries, and entries with unknown
// timestamps are never candidates.
func staleWorktrees(wts []worktree, cutoff time.Duration) []worktree {
	var out []worktree
	for _, wt := range wts {
		if wt.isMain || wt.pinned || wt.locked || wt.missing || wt.modified.IsZero() {
			continue
		}
		if time.Since(wt.modified) > cutoff {
			out = append(out, wt)
		}
	}
	return out
}

type appModel struct {
	root   string
	view   view
	mode   mode
	width  int
	height int

	allProjects []project
	projects    []project // filtered view of allProjects
	projCursor  int
	showHidden  bool
	filter      string
	hideHistory []string // paths hidden this session, for u to undo
	currentPath string   // project containing the cwd; pinned to the top
	cfg         config

	repo     string
	repoName string
	wts      []worktree
	wtCursor int
	sortBy   sortMode

	input        textinput.Model
	deleteTarget worktree
	forceErr     string
	pruneList    []string    // worktrees a prune would remove, shown for confirmation
	pruneProtect []string    // pinned worktree paths to lock while pruning
	sweepItems   []sweepItem // inactive worktrees P removes, with per-item status
	sweepIdx     int         // index of the sweep item being removed
	spin         spinner.Model

	status    string
	statusErr bool
	busy      bool
	chosen    string
}

func newApp(root, repo string) appModel {
	ti := textinput.New()
	ti.Placeholder = "branch name"
	ti.CharLimit = 120
	ti.Prompt = ""
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = warnStyle
	return appModel{root: root, input: ti, spin: sp, cfg: loadConfig(), currentPath: repo}
}

func (m appModel) Init() tea.Cmd {
	return loadProjects(m.root)
}

type projectsMsg struct {
	projects []project
	err      error
}

type worktreesMsg struct {
	wts []worktree
	err error
}

type gitDoneMsg struct {
	action string
	err    error
}

type pruneListMsg struct {
	names []string
	err   error
}

type sweepDoneMsg struct {
	idx int
	err error
}

type updateDoneMsg struct {
	tag     string
	updated bool
	err     error
}

func runSelfUpdate() tea.Cmd {
	return func() tea.Msg {
		tag, updated, err := selfUpdate()
		return updateDoneMsg{tag, updated, err}
	}
}

// removeStale removes one stale worktree and reports back which one.
func removeStale(repo string, wt worktree, idx int) tea.Cmd {
	return func() tea.Msg {
		return sweepDoneMsg{idx, removeWorktree(repo, wt.path, false)}
	}
}

func loadPrunable(repo string) tea.Cmd {
	return func() tea.Msg {
		names, err := prunableWorktrees(repo)
		return pruneListMsg{names, err}
	}
}

func loadProjects(root string) tea.Cmd {
	return func() tea.Msg {
		ps, err := scanProjects(root)
		return projectsMsg{ps, err}
	}
}

func loadWorktrees(repo string) tea.Cmd {
	return func() tea.Msg {
		wts, err := listWorktrees(repo)
		return worktreesMsg{wts, err}
	}
}

func runGit(action string, fn func() error) tea.Cmd {
	return func() tea.Msg {
		return gitDoneMsg{action, fn()}
	}
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case projectsMsg:
		if msg.err != nil {
			m.status, m.statusErr = firstLine(msg.err.Error()), true
			return m, nil
		}
		m.allProjects = msg.projects
		m.resolveCurrent()
		m.applyFilter()
		return m, nil

	case worktreesMsg:
		if msg.err != nil {
			m.status, m.statusErr = firstLine(msg.err.Error()), true
			return m, nil
		}
		m.wts = msg.wts
		for i := range m.wts {
			m.wts[i].pinned = m.cfg.isPinned(m.wts[i].path)
		}
		m.sortWts()
		m.wtCursor = clamp(m.wtCursor, 0, len(m.wts)-1)
		return m, nil

	case pruneListMsg:
		m.busy = false
		if msg.err != nil {
			m.status, m.statusErr = firstLine(msg.err.Error()), true
			return m, nil
		}
		// pinned worktrees are withheld from the prune and locked during it
		var names, protect []string
		for _, name := range msg.names {
			pinned := false
			for _, wt := range m.wts {
				if wt.name == name && wt.pinned && wt.path != "" {
					pinned, protect = true, append(protect, wt.path)
					break
				}
			}
			if !pinned {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			m.status, m.statusErr = "nothing to prune", false
			if len(protect) > 0 {
				m.status = fmt.Sprintf("nothing to prune (%d pinned)", len(protect))
			}
			return m, nil
		}
		m.pruneList, m.pruneProtect = names, protect
		m.mode = modeConfirmPrune
		m.status = ""
		return m, nil

	case spinner.TickMsg:
		if m.mode != modeSweeping {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case sweepDoneMsg:
		if m.mode != modeSweeping || msg.idx >= len(m.sweepItems) {
			return m, nil
		}
		if msg.err != nil {
			m.sweepItems[msg.idx].state = sweepFailed
			m.sweepItems[msg.idx].errMsg = firstLine(msg.err.Error())
		} else {
			m.sweepItems[msg.idx].state = sweepRemoved
		}
		if next := msg.idx + 1; next < len(m.sweepItems) {
			m.sweepIdx = next
			m.sweepItems[next].state = sweepActive
			return m, removeStale(m.repo, m.sweepItems[next].wt, next)
		}
		removed, failed := 0, 0
		for _, it := range m.sweepItems {
			if it.state == sweepRemoved {
				removed++
			} else if it.state == sweepFailed {
				failed++
			}
		}
		m.mode = modeNormal
		m.sweepItems = nil
		if failed > 0 {
			m.status = fmt.Sprintf("removed %d, skipped %d (dirty or locked)", removed, failed)
			m.statusErr = true
		} else {
			m.status = fmt.Sprintf("removed %d stale worktrees", removed)
			m.statusErr = false
		}
		return m, loadWorktrees(m.repo)

	case updateDoneMsg:
		m.busy = false
		switch {
		case msg.err != nil:
			m.status, m.statusErr = "update failed: "+firstLine(msg.err.Error()), true
		case !msg.updated:
			m.status, m.statusErr = "already up to date ("+msg.tag+")", false
		default:
			m.status, m.statusErr = "updated to "+msg.tag+" — restart to apply", false
		}
		return m, nil

	case gitDoneMsg:
		m.busy = false
		if msg.err != nil {
			if msg.action == "remove" {
				// a dirty worktree needs --force; offer it
				m.forceErr = firstLine(msg.err.Error())
				m.mode = modeConfirmForce
				m.status = ""
				return m, nil
			}
			m.status, m.statusErr = firstLine(msg.err.Error()), true
			return m, loadWorktrees(m.repo)
		}
		m.status, m.statusErr = msg.action+" ✓", false
		return m, loadWorktrees(m.repo)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m appModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeCreate:
		switch msg.String() {
		case "esc":
			m.mode = modeNormal
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.input.Value())
			if name == "" {
				return m, nil
			}
			m.mode = modeNormal
			m.busy = true
			m.status = "creating " + name + "…"
			repo, base, sync := m.repo, m.cfg.defaultBranchFor(m.repo), m.cfg.syncFilesFor(m.repo)
			return m, runGit("created "+name, func() error { return createWorktree(repo, name, base, sync) })
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case modeConfirmDelete:
		m.mode = modeNormal
		if s := msg.String(); s == "y" || s == "Y" {
			m.busy = true
			m.status = "removing " + m.deleteTarget.name + "…"
			repo, path := m.repo, m.deleteTarget.path
			return m, runGit("remove", func() error { return removeWorktree(repo, path, false) })
		}
		return m, nil

	case modeConfirmPrune:
		m.mode = modeNormal
		m.pruneList = nil
		protect := m.pruneProtect
		m.pruneProtect = nil
		if s := msg.String(); s == "y" || s == "Y" {
			m.busy = true
			m.status = "pruning…"
			repo := m.repo
			return m, runGit("pruned", func() error { return pruneWorktreesProtecting(repo, protect) })
		}
		return m, nil

	case modeConfirmSweep:
		if s := msg.String(); s == "y" || s == "Y" {
			m.mode = modeSweeping
			m.sweepIdx = 0
			m.sweepItems[0].state = sweepActive
			m.status = ""
			return m, tea.Batch(m.spin.Tick, removeStale(m.repo, m.sweepItems[0].wt, 0))
		}
		m.mode = modeNormal
		m.sweepItems = nil
		return m, nil

	case modeSweeping:
		// the cleanup job is running; only quitting is allowed
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
		return m, nil

	case modeFilter:
		switch msg.String() {
		case "esc":
			m.mode = modeNormal
			m.filter = ""
			m.applyFilter()
			return m, nil
		case "enter":
			m.mode = modeNormal
			return m.openProject()
		case "up", "ctrl+p":
			m.projCursor = clamp(m.projCursor-1, 0, len(m.projects)-1)
			return m, nil
		case "down", "ctrl+n":
			m.projCursor = clamp(m.projCursor+1, 0, len(m.projects)-1)
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if v := m.input.Value(); v != m.filter {
			m.filter = v
			m.projCursor = 0
			m.applyFilter()
		}
		return m, cmd

	case modeConfirmForce:
		m.mode = modeNormal
		if s := msg.String(); s == "y" || s == "Y" {
			m.busy = true
			m.status = "force removing " + m.deleteTarget.name + "…"
			repo, path := m.repo, m.deleteTarget.path
			return m, runGit("removed "+m.deleteTarget.name, func() error { return removeWorktree(repo, path, true) })
		}
		m.status, m.statusErr = m.forceErr, true
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "U":
		if m.busy {
			return m, nil
		}
		m.busy = true
		m.status = "checking for updates…"
		return m, runSelfUpdate()
	}
	if m.view == viewProjects {
		return m.handleProjectsKey(msg)
	}
	return m.handleWorktreesKey(msg)
}

func (m appModel) handleProjectsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.projects) - 1
	switch msg.String() {
	case "up", "k":
		m.projCursor = clamp(m.projCursor-1, 0, last)
	case "down", "j":
		m.projCursor = clamp(m.projCursor+1, 0, last)
	case "g", "home":
		m.projCursor = 0
	case "G", "end":
		m.projCursor = max(0, last)
	case "r":
		return m, loadProjects(m.root)
	case "/":
		m.mode = modeFilter
		m.input.Placeholder = "type to filter"
		m.input.SetValue(m.filter)
		m.input.Focus()
		m.status = ""
		return m, textinput.Blink
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.applyFilter()
		}
	case "x":
		if len(m.projects) == 0 {
			return m, nil
		}
		p := m.projects[m.projCursor]
		nowHidden := m.cfg.toggleHidden(p.path)
		if err := saveConfig(m.cfg); err != nil {
			m.status, m.statusErr = "saving config: "+firstLine(err.Error()), true
			return m, nil
		}
		m.applyFilter()
		m.statusErr = false
		if nowHidden {
			m.hideHistory = append(m.hideHistory, p.path)
			m.status = "hid " + p.name + " — u to undo"
			if !m.showHidden {
				m.status += " · . to show hidden"
			}
		} else {
			m.status = "unhid " + p.name
		}
	case "u":
		return m.undoHide()
	case ".":
		m.showHidden = !m.showHidden
		m.applyFilter()
		m.status = ""
	case "enter":
		return m.openProject()
	}
	return m, nil
}

// undoHide unhides the most recently hidden project of this session,
// skipping entries the user has already unhidden by hand.
func (m appModel) undoHide() (tea.Model, tea.Cmd) {
	for len(m.hideHistory) > 0 {
		path := m.hideHistory[len(m.hideHistory)-1]
		m.hideHistory = m.hideHistory[:len(m.hideHistory)-1]
		if !m.cfg.isHidden(path) {
			continue
		}
		m.cfg.toggleHidden(path)
		if err := saveConfig(m.cfg); err != nil {
			m.status, m.statusErr = "saving config: "+firstLine(err.Error()), true
			return m, nil
		}
		m.applyFilter()
		name := filepath.Base(path)
		for i, p := range m.projects {
			if p.path == path {
				m.projCursor = i
				break
			}
		}
		m.status, m.statusErr = "restored "+name, false
		return m, nil
	}
	m.status, m.statusErr = "nothing to undo", false
	return m, nil
}

// openProject switches to the worktree view for the project under the cursor.
func (m appModel) openProject() (tea.Model, tea.Cmd) {
	if len(m.projects) == 0 {
		return m, nil
	}
	p := m.projects[m.projCursor]
	m.view = viewWorktrees
	m.repo, m.repoName = p.path, p.name
	m.wts = nil
	m.wtCursor = 0
	m.status = ""
	return m, loadWorktrees(p.path)
}

func (m appModel) handleWorktreesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.wts) - 1
	switch msg.String() {
	case "up", "k":
		m.wtCursor = clamp(m.wtCursor-1, 0, last)
	case "down", "j":
		m.wtCursor = clamp(m.wtCursor+1, 0, last)
	case "g", "home":
		m.wtCursor = 0
	case "G", "end":
		m.wtCursor = max(0, last)
	case "enter":
		if wt, ok := m.currentWt(); ok {
			if wt.missing {
				m.status, m.statusErr = "worktree directory is missing — press p to prune", true
				return m, nil
			}
			m.chosen = wt.path
			return m, tea.Quit
		}
	case "n":
		m.mode = modeCreate
		m.input.Placeholder = "branch name"
		m.input.SetValue("")
		m.input.Focus()
		m.status = ""
		return m, textinput.Blink
	case "d":
		wt, ok := m.currentWt()
		if !ok {
			return m, nil
		}
		switch {
		case wt.isMain:
			m.status, m.statusErr = "cannot remove the main worktree", true
		case wt.pinned:
			m.status, m.statusErr = "worktree is pinned — press * to unpin first", true
		case wt.missing:
			m.status, m.statusErr = "worktree directory is missing — press p to prune", true
		default:
			m.deleteTarget = wt
			m.mode = modeConfirmDelete
			m.status = ""
		}
	case "*":
		wt, ok := m.currentWt()
		if !ok {
			return m, nil
		}
		if wt.isMain {
			m.status, m.statusErr = "the main worktree doesn't need pinning", false
			return m, nil
		}
		nowPinned := m.cfg.togglePinned(wt.path)
		if err := saveConfig(m.cfg); err != nil {
			m.status, m.statusErr = "saving config: "+firstLine(err.Error()), true
			return m, nil
		}
		m.wts[m.wtCursor].pinned = nowPinned
		m.statusErr = false
		if nowPinned {
			m.status = "pinned " + wt.branch + " — protected from prune and sweep"
		} else {
			m.status = "unpinned " + wt.branch
		}
	case "p":
		m.busy = true
		m.status = "checking for stale worktrees…"
		return m, loadPrunable(m.repo)
	case "P":
		stale := staleWorktrees(m.wts, sweepCutoff)
		if len(stale) == 0 {
			m.status, m.statusErr = "no worktrees inactive for over a month", false
			return m, nil
		}
		m.sweepItems = make([]sweepItem, len(stale))
		for i, wt := range stale {
			m.sweepItems[i] = sweepItem{wt: wt}
		}
		m.mode = modeConfirmSweep
		m.status = ""
	case "s":
		if m.sortBy == sortModified {
			m.sortBy = sortCreated
		} else {
			m.sortBy = sortModified
		}
		m.sortWts()
		m.wtCursor = 0
	case "r":
		return m, loadWorktrees(m.repo)
	case "esc", "backspace":
		m.view = viewProjects
		m.status = ""
		return m, loadProjects(m.root)
	}
	return m, nil
}

// resolveCurrent normalizes currentPath to the scanned project it matches
// (resolving symlinks), or synthesizes an entry when the cwd's repo lives
// outside the scan root, so it can still be pinned.
func (m *appModel) resolveCurrent() {
	if m.currentPath == "" {
		return
	}
	want, _ := filepath.EvalSymlinks(m.currentPath)
	for _, p := range m.allProjects {
		got, _ := filepath.EvalSymlinks(p.path)
		if p.path == m.currentPath || (want != "" && got == want) {
			m.currentPath = p.path
			return
		}
	}
	n, recent := scanWorktreeNames(m.currentPath)
	m.allProjects = append([]project{{
		name: filepath.Base(m.currentPath), path: m.currentPath, worktrees: n, recent: recent,
	}}, m.allProjects...)
}

// applyFilter rebuilds the visible project list from allProjects, honoring
// the hidden list, the show-hidden toggle, and the fuzzy filter. With a
// filter active, projects are ordered best match first. The current project
// is always visible and pinned to the top.
func (m *appModel) applyFilter() {
	vis := make([]project, 0, len(m.allProjects))
	scores := make(map[string]int)
	for _, p := range m.allProjects {
		p.hidden = m.cfg.isHidden(p.path)
		if p.hidden && !m.showHidden && p.path != m.currentPath {
			continue
		}
		sc, ok := fuzzyScore(p.name, m.filter)
		if !ok {
			continue
		}
		scores[p.path] = sc
		vis = append(vis, p)
	}
	if m.filter != "" {
		sort.SliceStable(vis, func(i, j int) bool { return scores[vis[i].path] > scores[vis[j].path] })
	}
	for i, p := range vis {
		if p.path == m.currentPath && i > 0 {
			copy(vis[1:i+1], vis[:i])
			vis[0] = p
			break
		}
	}
	m.projects = vis
	m.projCursor = clamp(m.projCursor, 0, len(m.projects)-1)
}

func (m appModel) hiddenCount() int {
	n := 0
	for _, p := range m.allProjects {
		if m.cfg.isHidden(p.path) {
			n++
		}
	}
	return n
}

// footerExtra is how many lines the footer grows beyond its usual two, so
// the list view can shrink and keep everything on screen.
func (m appModel) footerExtra() int {
	var n int
	switch m.mode {
	case modeConfirmPrune:
		n = len(m.pruneList)
	case modeConfirmSweep, modeSweeping:
		n = len(m.sweepItems)
	default:
		return 0
	}
	extra := min(n, pruneShowMax)
	if n > pruneShowMax {
		extra++
	}
	return extra
}

// viewSweepProgress renders the live status of the 1-by-1 cleanup job:
// done entries green, the in-flight one red with a spinner, the rest dim.
func (m appModel) viewSweepProgress() string {
	n := len(m.sweepItems)
	done := 0
	for _, it := range m.sweepItems {
		if it.state == sweepRemoved || it.state == sweepFailed {
			done++
		}
	}
	lines := []string{accentStyle.Render(
		fmt.Sprintf("removing stale worktrees… %d/%d", min(done+1, n), n))}

	// keep the active item in a window of pruneShowMax rows
	start := clamp(m.sweepIdx-pruneShowMax+2, 0, max(0, n-pruneShowMax))
	textW := max(20, m.width-6)
	for i := start; i < min(n, start+pruneShowMax); i++ {
		it := m.sweepItems[i]
		label := truncate(it.wt.branch, textW)
		var line string
		switch it.state {
		case sweepRemoved:
			line = goodStyle.Render("  ✓ " + label + "  removed")
		case sweepFailed:
			line = warnStyle.Render("  ✗ "+label) +
				dimStyle.Render("  "+truncate(it.errMsg, max(10, textW-len([]rune(label)))))
		case sweepActive:
			line = "  " + m.spin.View() + " " + warnStyle.Render(label+"  deleting…")
		default:
			line = dimStyle.Render("    " + label)
		}
		lines = append(lines, line)
	}
	if rest := n - (start + pruneShowMax); rest > 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  … %d more", rest)))
	}
	return strings.Join(lines, "\n")
}

func (m appModel) currentWt() (worktree, bool) {
	if m.wtCursor < 0 || m.wtCursor >= len(m.wts) {
		return worktree{}, false
	}
	return m.wts[m.wtCursor], true
}

// sortWts orders linked worktrees by the active sort key, keeping the main
// worktree pinned to the top.
func (m *appModel) sortWts() {
	if len(m.wts) < 3 {
		return
	}
	rest := m.wts[1:]
	sort.SliceStable(rest, func(i, j int) bool {
		if m.sortBy == sortCreated {
			return rest[i].created.After(rest[j].created)
		}
		return rest[i].modified.After(rest[j].modified)
	})
}

// --- rendering ---

func (m appModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	var body string
	if m.view == viewProjects {
		body = m.viewProjectList()
	} else {
		body = m.viewWorktreeList()
	}
	header := m.viewHeader()
	footer := m.viewFooter()

	filterBar := ""
	if m.showFilterBar() {
		filterBar = m.viewFilterBar() + "\n\n"
	}
	content := header + "\n" + filterBar + body
	avail := m.height - lipgloss.Height(footer)
	if h := lipgloss.Height(content); h < avail {
		content += strings.Repeat("\n", avail-h)
	} else if !strings.HasSuffix(content, "\n") {
		// body filled the screen exactly: keep the footer off its last line
		content += "\n"
	}
	return content + footer
}

// showFilterBar reports whether the filter line should render above the
// project list: while typing a filter, or while one is applied.
func (m appModel) showFilterBar() bool {
	return m.view == viewProjects && (m.mode == modeFilter || m.filter != "")
}

func (m appModel) viewFilterBar() string {
	if m.mode == modeFilter {
		return accentStyle.Render("filter ") + m.input.View()
	}
	return accentStyle.Render("filter ") + titleStyle.Render(m.filter) +
		subtleStyle.Render("   / edit · esc clear")
}

func (m appModel) viewHeader() string {
	logo := logoStyle.Render("arbr")
	var ctx string
	if m.view == viewProjects {
		hidden := ""
		if hc := m.hiddenCount(); hc > 0 {
			hidden = fmt.Sprintf(" · %d hidden", hc)
			if m.showHidden {
				hidden += " (shown)"
			}
		}
		ctx = titleStyle.Render(displayPath(m.root)) +
			subtleStyle.Render(fmt.Sprintf("   %d projects%s", len(m.projects), hidden))
	} else {
		n := max(0, len(m.wts)-1)
		noun := "worktrees"
		if n == 1 {
			noun = "worktree"
		}
		ctx = nameStyle(m.repoName).Render(m.repoName) +
			subtleStyle.Render(fmt.Sprintf("   %d %s · by %s", n, noun, m.sortBy))
	}
	return logo + "  " + ctx + "\n"
}

// projectBlock renders one project entry: name, divider, and a preview of
// its most recently modified worktrees.
func (m appModel) projectBlock(i int) []string {
	p := m.projects[i]
	textW := max(20, m.width-6)
	name := truncate(p.name, textW)

	marker, nameSt := "  ", nameStyle(p.name)
	if p.hidden {
		nameSt = dimStyle
	}
	if i == m.projCursor {
		marker = selectedRowStyle.Render("▸ ")
		nameSt = nameSt.Underline(true)
	}
	circle := ""
	if p.path == m.currentPath && m.currentPath != "" {
		circle = lipgloss.NewStyle().Foreground(nameColor(p.name)).Render("● ")
	}
	title := marker + circle + nameSt.Render(name)
	if p.hidden {
		title += dimStyle.Render("  hidden")
	}

	lines := []string{title}
	shown := min(len(p.recent), previewMax)
	for _, wt := range p.recent[:shown] {
		lines = append(lines, "  "+dimStyle.Render(truncate(wt, textW)))
	}
	if more := p.worktrees - shown; more > 0 {
		lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("… %d more", more)))
	}
	if p.worktrees == 0 {
		lines = append(lines, "  "+subtleStyle.Render("no worktrees"))
	}
	// full-width rule so adjacent projects read as separate entries
	return append(lines, subtleStyle.Render(strings.Repeat("─", max(20, m.width-2))))
}

func (m appModel) viewProjectList() string {
	if len(m.projects) == 0 {
		if m.filter != "" {
			return dimStyle.Render("  no projects match “" + m.filter + "”")
		}
		return dimStyle.Render("  no git projects found in " + displayPath(m.root))
	}

	blocks := make([][]string, len(m.projects))
	for i := range m.projects {
		blocks[i] = m.projectBlock(i)
	}

	// -7 leaves room for the header, footer, and the page indicator line
	avail := max(4, m.height-7)
	if m.showFilterBar() {
		avail = max(4, avail-2)
	}
	// walk back from the cursor so its block is always fully on screen
	start, used := m.projCursor, len(blocks[m.projCursor])
	for start > 0 && used+len(blocks[start-1]) <= avail {
		start--
		used += len(blocks[start])
	}
	var lines []string
	total, end := 0, start
	for i := start; i < len(blocks); i++ {
		if total+len(blocks[i]) > avail {
			if i != start {
				break
			}
			// a single block taller than the viewport: truncate it
			lines = append(lines, blocks[i][:avail]...)
			total, end = avail, i+1
			break
		}
		lines = append(lines, blocks[i]...)
		total += len(blocks[i])
		end = i + 1
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if start > 0 || end < len(m.projects) {
		out += "\n" + dimStyle.Render(fmt.Sprintf("  %d/%d", m.projCursor+1, len(m.projects)))
	}
	return out
}

func (m appModel) viewWorktreeList() string {
	if len(m.wts) == 0 {
		return dimStyle.Render("  loading…")
	}
	branchW := 6
	for _, wt := range m.wts {
		branchW = max(branchW, len([]rune(wt.branch)))
	}
	branchW = min(branchW, 34)
	const ageW = 10
	// whatever width is left (minus room for tags) goes to the path column
	pathW := min(m.width-(branchW+2*ageW+14)-10, 60)
	showPath := pathW >= 8

	sortInd := func(s sortMode) string {
		if m.sortBy == s {
			return " ▾"
		}
		return ""
	}
	header := "  " + subtleStyle.Render(pad("BRANCH", branchW))
	if showPath {
		header += "  " + subtleStyle.Render(pad("PATH", pathW))
	}
	header += "  " + subtleStyle.Render(
		pad("CREATED"+sortInd(sortCreated), ageW+2)+"  "+
			pad("MODIFIED"+sortInd(sortModified), ageW+2))

	var pathCols []string
	if showPath {
		raw := make([]string, len(m.wts))
		for i, wt := range m.wts {
			raw[i] = wt.path
			if wt.path == "" {
				raw[i] = "—"
			}
		}
		pathCols = shortenPaths(raw, pathW)
	}

	// -8: header(2) + join(1) + column header(2) + page indicator(1) + footer(2)
	visible := max(3, m.height-8-m.footerExtra())
	start := 0
	if m.wtCursor >= visible {
		start = m.wtCursor - visible + 1
	}
	end := min(len(m.wts), start+visible)

	lines := []string{header, ""}
	for i := start; i < end; i++ {
		wt := m.wts[i]
		branch := pad(truncate(wt.branch, branchW), branchW)
		created := pad(ago(wt.created), ageW+2)
		modified := pad(ago(wt.modified), ageW+2)

		var tags []string
		if wt.isMain {
			tags = append(tags, goodStyle.Render("main"))
		}
		if wt.pinned {
			tags = append(tags, accentStyle.Render("pinned"))
		}
		if wt.locked {
			tags = append(tags, dimStyle.Render("locked"))
		}
		if wt.missing {
			tags = append(tags, warnStyle.Render("missing"))
		}
		tag := ""
		if len(tags) > 0 {
			tag = "  " + strings.Join(tags, " ")
		}

		createdCol := ageStyle(wt.created).Render(created)
		modifiedCol := ageStyle(wt.modified).Render(modified)
		pathCol := ""
		if showPath {
			pathCol = "  " + dimStyle.Render(pad(pathCols[i], pathW))
		}
		var line string
		if i == m.wtCursor {
			line = selectedRowStyle.Render("▸ "+branch) + pathCol + "  " +
				createdCol + "  " + modifiedCol + tag
		} else {
			line = "  " + branch + pathCol + "  " +
				createdCol + "  " + modifiedCol + tag
		}
		lines = append(lines, line)
	}
	if len(m.wts) == 1 {
		lines = append(lines, "", dimStyle.Render("  no linked worktrees — press n to create one"))
	}
	if start > 0 || end < len(m.wts) {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %d/%d", m.wtCursor+1, len(m.wts))))
	}
	return strings.Join(lines, "\n")
}

func (m appModel) viewFooter() string {
	var top string
	switch m.mode {
	case modeCreate:
		top = accentStyle.Render("new worktree ") + subtleStyle.Render("(branch name) ") + m.input.View()
	case modeConfirmDelete:
		top = warnStyle.Render(fmt.Sprintf("remove worktree %q? (y/N)", m.deleteTarget.name))
	case modeConfirmPrune:
		n := len(m.pruneList)
		noun := "entries"
		if n == 1 {
			noun = "entry"
		}
		lines := []string{warnStyle.Render(fmt.Sprintf("prune %d stale worktree %s? (y/N)", n, noun))}
		for _, name := range m.pruneList[:min(n, pruneShowMax)] {
			detail := name
			for _, wt := range m.wts {
				if wt.name == name && wt.path != "" {
					detail += "  " + displayPath(wt.path)
					break
				}
			}
			lines = append(lines, dimStyle.Render("  "+truncate(detail, max(20, m.width-4))))
		}
		if n > pruneShowMax {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  … %d more", n-pruneShowMax)))
		}
		top = strings.Join(lines, "\n")
	case modeConfirmSweep:
		n := len(m.sweepItems)
		noun := "worktrees"
		if n == 1 {
			noun = "worktree"
		}
		lines := []string{warnStyle.Render(
			fmt.Sprintf("remove %d %s with no activity in the last month? (y/N)", n, noun))}
		for _, it := range m.sweepItems[:min(n, pruneShowMax)] {
			detail := fmt.Sprintf("%s  %s  %s", it.wt.branch, ago(it.wt.modified), displayPath(it.wt.path))
			lines = append(lines, dimStyle.Render("  "+truncate(detail, max(20, m.width-4))))
		}
		if n > pruneShowMax {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  … %d more", n-pruneShowMax)))
		}
		top = strings.Join(lines, "\n")
	case modeSweeping:
		top = m.viewSweepProgress()
	case modeConfirmForce:
		top = warnStyle.Render(truncate(
			fmt.Sprintf("remove failed: %s — force remove? (y/N)", m.forceErr), max(20, m.width-2)))
	default:
		switch {
		case m.busy:
			top = dimStyle.Render(m.status)
		case m.status != "" && m.statusErr:
			top = warnStyle.Render(truncate(m.status, max(20, m.width-2)))
		case m.status != "":
			top = goodStyle.Render(m.status)
		}
	}

	var help string
	if m.mode == modeNormal {
		if m.view == viewProjects {
			hideDesc := "hide"
			if len(m.projects) > 0 && m.projects[m.projCursor].hidden {
				hideDesc = "unhide"
			}
			showDesc := "show hidden"
			if m.showHidden {
				showDesc = "mask hidden"
			}
			entries := []string{
				helpEntry("↑↓", "move"),
				helpEntry("enter", "open"),
				helpEntry("/", "filter"),
				helpEntry("x", hideDesc),
			}
			if len(m.hideHistory) > 0 {
				entries = append(entries, helpEntry("u", "undo hide"))
			}
			entries = append(entries,
				helpEntry(".", showDesc),
				helpEntry("r", "refresh"),
				helpEntry("U", "update"),
				helpEntry("q", "quit"),
			)
			help = strings.Join(entries, subtleStyle.Render(" · "))
		} else {
			help = strings.Join([]string{
				helpEntry("enter", "cd"),
				helpEntry("n", "new"),
				helpEntry("d", "delete"),
				helpEntry("p", "prune"),
				helpEntry("P", "sweep stale"),
				helpEntry("*", "pin"),
				helpEntry("s", "sort"),
				helpEntry("U", "update"),
				helpEntry("esc", "projects"),
				helpEntry("q", "quit"),
			}, subtleStyle.Render(" · "))
		}
	} else if m.mode == modeFilter {
		help = subtleStyle.Render("↑↓ move · enter open · esc clear")
	} else if m.mode == modeSweeping {
		help = subtleStyle.Render("removing one at a time · q quit")
	} else {
		help = subtleStyle.Render("esc cancel")
	}
	return top + "\n" + help
}

// --- small helpers ---

func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, ok := strings.CutPrefix(p, home); ok {
			return "~" + rel
		}
	}
	return p
}

// collapsePath abbreviates every leading directory to one letter, keeping
// the immediate parent and leaf whole: ~/git/backend__worktrees/x →
// ~/g/backend__worktrees/x.
func collapsePath(s string) string {
	parts := strings.Split(s, "/")
	for i := 0; i < len(parts)-2; i++ {
		if parts[i] == "~" || parts[i] == "" {
			continue
		}
		if r := []rune(parts[i]); len(r) > 1 {
			parts[i] = string(r[:1])
		}
	}
	return strings.Join(parts, "/")
}

// shortenPaths renders paths with ~ for home, fitting width w. Collapsing
// is all-or-nothing: if any path is too long, every path is collapsed the
// same way, so the column stays visually consistent. Individual paths that
// still don't fit are …-truncated.
func shortenPaths(paths []string, w int) []string {
	display := make([]string, len(paths))
	collapse := false
	for i, p := range paths {
		display[i] = displayPath(p)
		if len([]rune(display[i])) > w {
			collapse = true
		}
	}
	for i, s := range display {
		if collapse {
			s = collapsePath(s)
		}
		display[i] = truncate(s, w)
	}
	return display
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func pad(s string, w int) string {
	r := []rune(s)
	if len(r) >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-len(r))
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}
