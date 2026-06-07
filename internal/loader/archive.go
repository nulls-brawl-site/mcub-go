// Package loader – archive.go
//
// ArchiveManager handles downloading and extracting zip/tar.gz archives that
// contain one or more Python modules.  It is the Go port of the Python
// ArchiveManager in core/lib/loader/archive.py.
package loader

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ArchiveModule represents a single Python module discovered inside an archive.
type ArchiveModule struct {
	// Name is the module name (file basename without .py).
	Name string
	// FilePath is the relative path inside the archive.
	FilePath string
	// Code is the full source text of the module.
	Code string
}

// ArchiveResult is the return type of ExtractArchive / DownloadAndExtract.
type ArchiveResult struct {
	// PackType is "single" for a one-module archive or "pack" for a
	// multi-module pack (contains pack.json).
	PackType string
	// Modules is the list of Python modules found.
	Modules []ArchiveModule
	// Manifest is non-nil when the archive contained a pack.json.
	Manifest *ArchiveManifest
}

// ArchiveManifest maps the pack.json inside a multi-module archive.
type ArchiveManifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Author      string   `json:"author"`
	Description string   `json:"description"`
	Modules     []string `json:"modules"`
	Requires    []string `json:"requires"`
}

// IsArchiveURL returns true when url looks like a zip archive.
// It matches a .zip extension (case-insensitive) or a URL that ends with a
// recognised archive format query parameter.
func IsArchiveURL(url string) bool {
	lower := strings.ToLower(url)
	for _, ext := range []string{".zip", ".tar.gz", ".tgz", ".tar"} {
		// Strip any query string before comparing.
		path := lower
		if idx := strings.Index(path, "?"); idx >= 0 {
			path = path[:idx]
		}
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// ParsePackManifest decodes pack.json bytes into an ArchiveManifest.
func ParsePackManifest(data []byte) (*ArchiveManifest, error) {
	var m ArchiveManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("archive: parse pack.json: %w", err)
	}
	return &m, nil
}

// DownloadAndExtract downloads the archive at url, saves it to destDir, and
// calls ExtractArchive to process it.
func DownloadAndExtract(url, destDir string) (*ArchiveResult, error) {
	resp, err := http.Get(url) // #nosec G107 – deliberate remote load
	if err != nil {
		return nil, fmt.Errorf("archive: download %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archive: download %q: HTTP %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("archive: read body from %q: %w", url, err)
	}

	// Pick a filename from the URL path.
	base := filepath.Base(strings.SplitN(url, "?", 2)[0])
	if base == "" || base == "." {
		base = "archive.zip"
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("archive: create dest dir %q: %w", destDir, err)
	}

	archivePath := filepath.Join(destDir, base)
	if err := os.WriteFile(archivePath, data, 0o644); err != nil {
		return nil, fmt.Errorf("archive: write archive file %q: %w", archivePath, err)
	}

	return ExtractArchive(archivePath, destDir)
}

// ExtractArchive extracts the archive at source into destDir and returns an
// ArchiveResult describing the contents.
//
// source may be a local file path or a URL.  For URLs the archive is
// downloaded first (delegates to DownloadAndExtract).
func ExtractArchive(source string, destDir string) (*ArchiveResult, error) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return DownloadAndExtract(source, destDir)
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("archive: read %q: %w", source, err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("archive: create dest dir %q: %w", destDir, err)
	}

	// Try zip first, then tar.gz.
	if isZip(data) {
		return extractZip(data, destDir)
	}
	return extractTar(data, destDir)
}

// ---- internal helpers -------------------------------------------------------

func isZip(data []byte) bool {
	return len(data) >= 4 &&
		data[0] == 0x50 && data[1] == 0x4B && // PK
		(data[2] == 0x03 || data[2] == 0x05 || data[2] == 0x07)
}

// safeExtractPath validates that the archive member path does not escape
// destDir (path traversal protection).  Returns the absolute target path or
// an error.
func safeExtractPath(destDir, memberName string) (string, error) {
	if strings.Contains(memberName, "..") ||
		strings.HasPrefix(memberName, "/") ||
		strings.HasPrefix(memberName, "\\") {
		return "", fmt.Errorf("archive: path traversal detected in %q", memberName)
	}
	target := filepath.Join(destDir, memberName)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("archive: resolve path %q: %w", target, err)
	}
	absDestDir, err := filepath.Abs(destDir)
	if err != nil {
		return "", fmt.Errorf("archive: resolve destDir %q: %w", destDir, err)
	}
	if absTarget != absDestDir && !strings.HasPrefix(absTarget, absDestDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive: path traversal detected in %q", memberName)
	}
	return absTarget, nil
}

func extractZip(data []byte, destDir string) (*ArchiveResult, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("archive: open zip: %w", err)
	}

	// Collect raw file contents indexed by relative path.
	files := make(map[string][]byte)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		target, err := safeExtractPath(destDir, f.Name)
		if err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("archive: open zip member %q: %w", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("archive: read zip member %q: %w", f.Name, err)
		}
		// Write to disk.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("archive: mkdir for %q: %w", target, err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return nil, fmt.Errorf("archive: write %q: %w", target, err)
		}
		files[f.Name] = body
	}

	return buildResult(files)
}

func extractTar(data []byte, destDir string) (*ArchiveResult, error) {
	// Try decompressing as gzip first.
	var tr *tar.Reader
	gr, gzErr := gzip.NewReader(bytes.NewReader(data))
	if gzErr == nil {
		tr = tar.NewReader(gr)
	} else {
		tr = tar.NewReader(bytes.NewReader(data))
	}

	files := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive: read tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // skip symlinks, devices, etc.
		}
		target, err := safeExtractPath(destDir, hdr.Name)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("archive: read tar member %q: %w", hdr.Name, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("archive: mkdir for %q: %w", target, err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return nil, fmt.Errorf("archive: write %q: %w", target, err)
		}
		files[hdr.Name] = body
	}

	if gzErr == nil {
		gr.Close()
	}
	return buildResult(files)
}

// buildResult analyses the extracted file map to determine pack type and
// collect module metadata.
func buildResult(files map[string][]byte) (*ArchiveResult, error) {
	result := &ArchiveResult{}

	// Check for pack.json.
	for name, body := range files {
		base := filepath.Base(name)
		if base == "pack.json" {
			manifest, err := ParsePackManifest(body)
			if err != nil {
				return nil, err
			}
			result.Manifest = manifest
			result.PackType = "pack"
			break
		}
	}

	if result.PackType == "" {
		result.PackType = "single"
	}

	// Collect Python modules.
	for relPath, body := range files {
		if !strings.HasSuffix(relPath, ".py") {
			continue
		}
		base := filepath.Base(relPath)
		if strings.HasPrefix(base, "_") {
			continue // skip __init__.py, __pycache__, etc.
		}
		name := strings.TrimSuffix(base, ".py")
		result.Modules = append(result.Modules, ArchiveModule{
			Name:     name,
			FilePath: relPath,
			Code:     string(body),
		})
	}

	if len(result.Modules) == 0 {
		return nil, fmt.Errorf("archive: no Python modules found in archive")
	}

	return result, nil
}
