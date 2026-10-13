#!/usr/bin/env bash
# Installs or updates arbr: downloads the latest release binary into
# ~/.local/bin, clears Gatekeeper quarantine, and ensures ~/.local/bin is
# on PATH. Idempotent - re-run it any time to update; it no-ops when already
# on the latest version:
#
#   curl -fsSL https://raw.githubusercontent.com/fipppi/arbr/main/install.sh | bash
set -euo pipefail

REPO="fipppi/arbr"
BIN_DIR="${HOME}/.local/bin"
BIN="${BIN_DIR}/arbr"

fail() { echo "error: $*" >&2; exit 1; }

[[ "$(uname -s)" == "Darwin" ]] || fail "macOS only"
[[ "$(uname -m)" == "arm64" ]] || fail "Apple Silicon only (this Mac is $(uname -m))"

latest="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
	sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
[[ -n "$latest" ]] || fail "could not determine the latest release of ${REPO}"

current=""
[[ -x "$BIN" ]] && current="$("$BIN" --version 2>/dev/null || true)"
if [[ -n "$current" && "$current" == "$latest" ]]; then
	echo "already up to date: arbr ${current} at ${BIN}"
	exit 0
fi

echo "downloading arbr ${latest}…"
mkdir -p "$BIN_DIR"
curl -fsSL -o "$BIN" "https://github.com/${REPO}/releases/download/${latest}/arbr"
chmod +x "$BIN"

# whitelist in Gatekeeper: the binary is unsigned, so strip the quarantine
# attribute if the download picked one up
xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true

# ensure ~/.local/bin is on PATH for future shells
case ":${PATH}:" in
*":${BIN_DIR}:"*) ;;
*)
	rc="${HOME}/.zshrc"
	if ! grep -qs '\.local/bin' "$rc"; then
		printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >>"$rc"
	fi
	echo "added ~/.local/bin to PATH in ${rc} - restart your shell or run: source ${rc}"
	;;
esac

if [[ -n "$current" ]]; then
	echo "updated arbr ${current} → $("$BIN" --version) at ${BIN}"
else
	echo "installed arbr $("$BIN" --version) at ${BIN}"
fi

if ! grep -qs 'arbr init' "${HOME}/.zshrc"; then
	echo 'to let arbr change your working directory, add the shell wrapper:'
	echo '  echo '"'"'eval "$(command arbr init)"'"'"' >> ~/.zshrc && source ~/.zshrc'
fi
