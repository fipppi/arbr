package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const shellInit = `# arbr shell integration — add 'eval "$(command arbr init)"' or paste this into ~/.zshrc
arbr() {
    if [ "$1" = "init" ]; then
        command arbr "$@"
        return $?
    fi
    local tmp dir rc
    tmp="$(mktemp "${TMPDIR:-/tmp}/arbr-cd.XXXXXX")" || return 1
    command arbr --cd-file "$tmp" "$@"
    rc=$?
    dir="$(cat "$tmp" 2>/dev/null)"
    rm -f "$tmp"
    [ "$rc" -eq 0 ] || return "$rc"
    if [ -n "$dir" ] && [ -d "$dir" ]; then
        cd "$dir" || return 1
    fi
}
`

func main() {
	root := flag.String("root", defaultRoot(), "directory to scan for git projects")
	cdFile := flag.String("cd-file", "", "write the chosen directory to this file (used by the shell wrapper)")
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "V", false, "print version and exit (shorthand)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `arbr — git worktree manager

usage:
  arbr [-root DIR]   open the TUI (project grid, or worktrees if inside a repo)
  arbr init          print the shell wrapper that makes Enter cd into a worktree

flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if showVersion {
		fmt.Println(versionString())
		return
	}

	if flag.Arg(0) == "init" {
		fmt.Print(shellInit)
		return
	}

	app := newApp(*root, detectRepo())
	// TUI renders on stderr so stdout carries only the chosen path for the shell wrapper.
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "arbr:", err)
		os.Exit(1)
	}
	if m, ok := final.(appModel); ok && m.chosen != "" {
		if *cdFile != "" {
			if err := os.WriteFile(*cdFile, []byte(m.chosen+"\n"), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "arbr: writing cd file:", err)
				os.Exit(1)
			}
			return
		}
		// no --cd-file: print the path for the legacy stdout-capture wrapper
		fmt.Println(m.chosen)
		// stdout on a terminal means no shell wrapper captured the path
		if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintln(os.Stderr, `arbr: shell integration is not active, so your directory was not changed`)
			fmt.Fprintln(os.Stderr, `arbr: add   eval "$(command arbr init)"   to ~/.zshrc and restart your shell`)
		}
	}
}

func defaultRoot() string {
	if v := os.Getenv("ARBR_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "git")
}

// detectRepo returns the main repository path when the cwd is inside a git
// repo (resolving through linked worktrees), or "" otherwise.
func detectRepo() string {
	out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		gitDir = filepath.Join(cwd, gitDir)
	}
	return filepath.Dir(gitDir)
}
