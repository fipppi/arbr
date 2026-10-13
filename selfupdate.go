package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"
)

const selfRepo = "fipppi/arbr"

var version = "dev" // set via -ldflags "-X main.version=vX.Y.Z"

// versionString falls back to the module version so `go install ...@vX.Y.Z`
// builds (which get no ldflags) still report something meaningful.
func versionString() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

// ghToken finds a GitHub token: env vars first, then the gh CLI. The repo is
// public, so a token is optional — it just raises the API rate limit.
func ghToken() string {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func ghReq(token, path, accept string) ([]byte, error) {
	req, err := http.NewRequest("GET", "https://api.github.com"+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	hc := &http.Client{Timeout: 60 * time.Second}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == 401 {
		return nil, errors.New("GitHub rejected the token (401) - re-run `gh auth login` or refresh GH_TOKEN")
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub %d on %s", res.StatusCode, path)
	}
	return body, nil
}

// selfUpdate downloads the latest release binary and atomically replaces the
// running executable. Returns the latest tag and whether an update happened
// (false when already on the latest version - nothing is downloaded then).
func selfUpdate() (string, bool, error) {
	token := ghToken()
	rb, err := ghReq(token, "/repos/"+selfRepo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return "", false, err
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(rb, &rel); err != nil {
		return "", false, err
	}
	if rel.Tag == versionString() {
		return rel.Tag, false, nil
	}
	assetID := int64(-1)
	for _, a := range rel.Assets {
		if a.Name == "arbr" {
			assetID = a.ID
			break
		}
	}
	if assetID < 0 {
		return "", false, errors.New("no arbr binary on the latest release")
	}
	// the asset redirects to a signed storage URL; Go drops the Authorization
	// header on the cross-host hop, which is what the storage backend expects
	bin, err := ghReq(token, fmt.Sprintf("/repos/%s/releases/assets/%d", selfRepo, assetID), "application/octet-stream")
	if err != nil {
		return "", false, err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false, err
	}
	// write next to the target, then rename: atomic, and safe while running
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", false, err
	}
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", tmp).Run()
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return "", false, err
	}
	return rel.Tag, true, nil
}
