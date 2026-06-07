// Package version ports core/version.py — version detection, update checking,
// and module compatibility verification for the MCUB kernel.
package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Version is the current MCUB kernel version.
// Mirrors the VERSION constant in core/version.py.
const Version = "1.4.0"

// updateBaseURL is the raw GitHub URL used to fetch the latest version.txt.
const updateBaseURL = "https://raw.githubusercontent.com/nulls-brawl-site/MCUB-fork/refs/heads/main/"

// ─────────────────────────────────────────────────────────────────────────────
// Version helpers
// ─────────────────────────────────────────────────────────────────────────────

// GetVersion returns the current MCUB kernel version string.
func GetVersion() string {
	return Version
}

// DetectBranch returns the current git branch name, falling back to "main".
func DetectBranch() string {
	out, err := runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out == "" {
		return "main"
	}
	return out
}

// GetCommitSHA returns the short (7-char) git commit hash of HEAD.
// Returns "unknown" if git is unavailable.
func GetCommitSHA() string {
	out, err := runGit("rev-parse", "--short", "HEAD")
	if err != nil {
		return "unknown"
	}
	return out
}

// GetGitHubCommitURL returns the GitHub URL pointing to the current commit.
func GetGitHubCommitURL() string {
	sha, err := runGit("rev-parse", "HEAD")
	if err != nil || sha == "" {
		return ""
	}
	base := "https://github.com/nulls-brawl-site/MCUB-fork"
	return fmt.Sprintf("%s/commit/%s", base, sha)
}

// GetLatestVersion fetches the latest kernel version string from GitHub.
// Returns an error if the network request fails or the response is malformed.
func GetLatestVersion() (string, error) {
	branch := DetectBranch()
	url := updateBaseURL + branch + "/version.txt"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch version: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", fmt.Errorf("read version body: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// IsUpdateAvailable compares the current version with the latest published
// version.  Returns (true, latestVersion, nil) when an update is available.
func IsUpdateAvailable() (bool, string, error) {
	latest, err := GetLatestVersion()
	if err != nil {
		return false, "", err
	}
	cmp := compareVersions(Version, latest)
	return cmp < 0, latest, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Module compatibility (# scop: directives)
// ─────────────────────────────────────────────────────────────────────────────

// scopDirective is parsed from "# scop: <scope> <params>" lines.
type scopDirective struct {
	Scope  string
	Params string
}

// CheckModuleCompatibility inspects module source code for "# scop:" directives
// and verifies them against the current environment.
//
// Returns (true, "") when all directives are satisfied, or (false, reason) when
// at least one is not.
func CheckModuleCompatibility(code string) (bool, string) {
	directives := parseScopDirectives(code)
	if len(directives) == 0 {
		return true, ""
	}

	for _, d := range directives {
		switch d.Scope {
		case "kernel":
			ok, reason := checkKernelDirective(d.Params)
			if !ok {
				return false, reason
			}
		case "ffmpeg":
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				return false, "module requires ffmpeg to be installed on the system"
			}
		}
		// "inline" scope cannot be checked without a live kernel reference;
		// skip gracefully.
	}
	return true, ""
}

// parseScopDirectives extracts all "# scop: <scope> <params>" lines.
func parseScopDirectives(code string) []scopDirective {
	var out []scopDirective
	for _, line := range strings.Split(code, "\n") {
		stripped := strings.TrimSpace(line)
		if !strings.HasPrefix(stripped, "# scop:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(stripped, "# scop:"))
		idx := strings.IndexByte(rest, ' ')
		if idx == -1 {
			out = append(out, scopDirective{Scope: rest})
		} else {
			out = append(out, scopDirective{Scope: rest[:idx], Params: strings.TrimSpace(rest[idx+1:])})
		}
	}
	return out
}

// checkKernelDirective validates a "kernel" scop directive.
func checkKernelDirective(params string) (bool, string) {
	parts := strings.Fields(params)
	if len(parts) == 0 {
		return true, ""
	}
	switch parts[0] {
	case "min":
		if len(parts) < 2 {
			return true, ""
		}
		required := strings.TrimPrefix(parts[1], "v")
		if compareVersions(Version, required) < 0 {
			return false, fmt.Sprintf("module requires kernel version ≥ %s, current is %s", required, Version)
		}
	case "max":
		if len(parts) < 2 {
			return true, ""
		}
		required := strings.TrimPrefix(parts[1], "v")
		if compareVersions(Version, required) > 0 {
			return false, fmt.Sprintf("module requires kernel version ≤ %s, current is %s", required, Version)
		}
	default:
		// Exact version match
		spec := strings.TrimPrefix(parts[0], "v")
		if spec == "[__lastest__]" || spec == "[__latest__]" {
			// Requires latest — skip without live network check
			return true, ""
		}
		if compareVersions(Version, spec) != 0 {
			return false, fmt.Sprintf("module requires kernel version exactly %s, current is %s", spec, Version)
		}
	}
	return true, ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Version comparison utilities
// ─────────────────────────────────────────────────────────────────────────────

var nonNumeric = regexp.MustCompile(`[^0-9]`)

// parseVersion splits "1.2.3-beta" → [1, 2, 3, 0]
func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	result := make([]int, len(parts))
	for i, p := range parts {
		clean := nonNumeric.ReplaceAllString(p, "")
		n, _ := strconv.Atoi(clean)
		result[i] = n
	}
	return result
}

// compareVersions returns -1, 0, or 1 (v1 < v2, v1 == v2, v1 > v2).
func compareVersions(v1, v2 string) int {
	a := parseVersion(v1)
	b := parseVersion(v2)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		ai, bi := 0, 0
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

// ─────────────────────────────────────────────────────────────────────────────
// GitHub release info (JSON API)
// ─────────────────────────────────────────────────────────────────────────────

// ReleaseInfo contains basic metadata from the GitHub Releases API.
type ReleaseInfo struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"html_url"`
}

// GetLatestRelease fetches the latest GitHub release metadata.
func GetLatestRelease(owner, repo string) (*ReleaseInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}
	var info ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode release info: %w", err)
	}
	return &info, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal git helper
// ─────────────────────────────────────────────────────────────────────────────

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
