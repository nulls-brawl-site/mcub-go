// Package loader – repository.go
//
// RepositoryManager is the Go port of MCUB's Python RepositoryManager.
// It handles fetching module lists and downloading modules from remote
// repository URLs. Equivalent to core/lib/loader/repository.py.
package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Repository represents a single module repository.
type Repository struct {
	// URL is the base URL of the repository (no trailing slash).
	URL string

	// Name is the human-readable display name (fetched lazily from name.ini).
	Name string
}

// RepositoryManager manages module repository URLs and provides helpers for
// listing and downloading modules from those repositories.
type RepositoryManager struct {
	repos  []Repository
	client *http.Client
}

// NewRepositoryManager creates a RepositoryManager with a default HTTP client
// that has a 30-second total timeout.
func NewRepositoryManager() *RepositoryManager {
	return &RepositoryManager{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// validateURL performs basic SSRF protection checks on a repository URL.
// Only HTTPS is allowed; private/loopback addresses are rejected.
func validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "https" {
		return fmt.Errorf("only https:// repositories are allowed, got %q", parsed.Scheme)
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no hostname")
	}

	blocked := map[string]bool{
		"localhost":            true,
		"localhost.localdomain": true,
		"0.0.0.0":             true,
		"127.0.0.1":           true,
		"::1":                 true,
	}
	if blocked[strings.ToLower(host)] {
		return fmt.Errorf("internal hosts are not allowed: %q", host)
	}

	// Reject private/loopback IP addresses.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("private or reserved IP addresses are not allowed: %q", host)
		}
	}

	return nil
}

// GetRepoName returns a display name for a repository URL.
// It first tries to fetch <repoURL>/name.ini; on failure it returns the last
// URL path segment.
func (r *RepositoryManager) GetRepoName(repoURL string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nameURL := strings.TrimRight(repoURL, "/") + "/name.ini"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nameURL, nil)
	if err == nil {
		resp, err := r.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
				if err == nil {
					if name := strings.TrimSpace(string(data)); name != "" {
						return name
					}
				}
			}
		}
	}

	// Fall back to last path segment.
	parts := strings.Split(strings.TrimRight(repoURL, "/"), "/")
	if last := parts[len(parts)-1]; last != "" {
		return last
	}
	return repoURL
}

// GetModuleList fetches the list of available module names from a repository.
// It tries <repoURL>/modules.ini (newline-separated names) first, then falls
// back to <repoURL>/__index__.json ({"modules": [...]}).
func (r *RepositoryManager) GetModuleList(ctx context.Context, repoURL string) ([]string, error) {
	base := strings.TrimRight(repoURL, "/")

	// Try modules.ini first (original MCUB format).
	if mods, err := r.fetchModulesIni(ctx, base+"/modules.ini"); err == nil {
		return mods, nil
	}

	// Fall back to __index__.json.
	return r.fetchIndexJSON(ctx, base+"/__index__.json")
}

func (r *RepositoryManager) fetchModulesIni(ctx context.Context, u string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			result = append(result, name)
		}
	}
	return result, nil
}

func (r *RepositoryManager) fetchIndexJSON(ctx context.Context, u string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}
	var payload struct {
		Modules []string `json:"modules"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode __index__.json: %w", err)
	}
	return payload.Modules, nil
}

// DownloadModule downloads a single module's Python source from the repository.
// It fetches <repoURL>/<moduleName>.py and returns the source code as a string.
func (r *RepositoryManager) DownloadModule(ctx context.Context, repoURL, moduleName string) (string, error) {
	moduleURL := strings.TrimRight(repoURL, "/") + "/" + moduleName + ".py"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, moduleURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request for %q: %w", moduleURL, err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %q: %w", moduleURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %q returned HTTP %d", moduleURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MiB limit
	if err != nil {
		return "", fmt.Errorf("read body from %q: %w", moduleURL, err)
	}
	return string(data), nil
}

// AddRepo validates and adds a repository URL to the manager.
// It verifies the URL passes SSRF checks and that a module list can be fetched.
func (r *RepositoryManager) AddRepo(ctx context.Context, rawURL string) error {
	if err := validateURL(rawURL); err != nil {
		return fmt.Errorf("repository URL rejected: %w", err)
	}

	// Check for duplicates.
	normalized := strings.TrimRight(rawURL, "/")
	for _, existing := range r.repos {
		if strings.TrimRight(existing.URL, "/") == normalized {
			return fmt.Errorf("repository already exists: %q", rawURL)
		}
	}

	// Verify we can actually fetch a module list.
	mods, err := r.GetModuleList(ctx, normalized)
	if err != nil {
		return fmt.Errorf("cannot fetch module list from %q: %w", rawURL, err)
	}
	if len(mods) == 0 {
		return fmt.Errorf("repository %q returned an empty module list", rawURL)
	}

	name := r.GetRepoName(normalized)
	r.repos = append(r.repos, Repository{URL: normalized, Name: name})
	return nil
}

// RemoveRepo removes a repository by 0-based index.
func (r *RepositoryManager) RemoveRepo(index int) error {
	if index < 0 || index >= len(r.repos) {
		return fmt.Errorf("repository index %d out of range (have %d)", index, len(r.repos))
	}
	r.repos = append(r.repos[:index], r.repos[index+1:]...)
	return nil
}

// ListRepos returns a copy of the current repository list.
func (r *RepositoryManager) ListRepos() []Repository {
	result := make([]Repository, len(r.repos))
	copy(result, r.repos)
	return result
}
