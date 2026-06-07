// Package platform detects the runtime environment (Termux, WSL, Docker, etc.).
//
// Ported from utils/platform.py.
// SPDX-License-Identifier: MIT
package platform

import (
	"os"
	"runtime"
	"strings"
)

// ---- Public API -------------------------------------------------------------

// IsTermux returns true when running inside Termux on Android.
func IsTermux() bool {
	if os.Getenv("TERMUX_VERSION") != "" {
		return true
	}
	paths := []string{
		"/data/data/com.termux/files/usr",
		"/data/data/com.termux",
		"/usr/bin/termux-info",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// IsWSL returns true when running inside Windows Subsystem for Linux.
func IsWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	for _, file := range []string{"/proc/version", "/proc/sys/kernel/osrelease"} {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(data))
		if strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl") {
			return true
		}
	}
	return false
}

// IsDocker returns true when running inside a Docker container.
func IsDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	for _, file := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(data))
		if strings.Contains(lower, "docker") || strings.Contains(lower, "lxc") {
			return true
		}
	}
	return false
}

// GetPlatform returns a human-readable platform label.
// Possible values: "termux", "wsl", "wsl2", "docker", "linux", "darwin",
// "windows", "unknown".
func GetPlatform() string {
	if IsTermux() {
		return "termux"
	}
	if IsWSL() {
		// Distinguish WSL2 by a WSL2-specific file.
		if _, err := os.Stat("/mnt/wsl"); err == nil {
			return "wsl2"
		}
		return "wsl"
	}
	if IsDocker() {
		return "docker"
	}
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	case "windows":
		return "windows"
	default:
		return "unknown"
	}
}

// GetDistroName reads /etc/os-release and returns the distro NAME value.
// Returns an empty string on non-Linux platforms or when the file is absent.
func GetDistroName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "NAME=") {
			val := strings.TrimPrefix(line, "NAME=")
			val = strings.Trim(val, "\"'")
			return val
		}
	}
	return ""
}

// GetOSVersion reads /etc/os-release and returns the VERSION_ID, falling back
// to the kernel release from /proc/version when not available.
func GetOSVersion() string {
	data, err := os.ReadFile("/etc/os-release")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VERSION_ID=") {
				val := strings.TrimPrefix(line, "VERSION_ID=")
				val = strings.Trim(val, "\"'")
				if val != "" {
					return val
				}
			}
		}
	}
	// Fallback: /proc/version
	vdata, verr := os.ReadFile("/proc/version")
	if verr != nil {
		return ""
	}
	line := strings.TrimSpace(string(vdata))
	if idx := strings.Index(line, " version "); idx >= 0 {
		rest := line[idx+9:]
		if end := strings.IndexByte(rest, ' '); end >= 0 {
			return rest[:end]
		}
		return rest
	}
	return line
}

// GetArch returns the current CPU architecture in Go notation
// (e.g. "amd64", "arm64", "386", "arm").
func GetArch() string {
	return runtime.GOARCH
}

// GetGoVersion returns the Go runtime version string (e.g. "go1.21.5").
func GetGoVersion() string {
	return runtime.Version()
}

// PlatformInfo holds a structured snapshot of the current environment.
type PlatformInfo struct {
	Platform  string
	Distro    string
	OSVersion string
	Arch      string
	GoVersion string
	GOOS      string
}

// Detect returns a fully-populated PlatformInfo for the current environment.
func Detect() PlatformInfo {
	return PlatformInfo{
		Platform:  GetPlatform(),
		Distro:    GetDistroName(),
		OSVersion: GetOSVersion(),
		Arch:      GetArch(),
		GoVersion: GetGoVersion(),
		GOOS:      runtime.GOOS,
	}
}

// FriendlyName maps the platform tag to a human-readable label.
func FriendlyName() string {
	names := map[string]string{
		"termux":  "Termux (Android)",
		"wsl":     "Windows Subsystem for Linux",
		"wsl2":    "Windows Subsystem for Linux 2",
		"docker":  "Docker Container",
		"linux":   "Linux",
		"darwin":  "macOS",
		"windows": "Windows",
		"unknown": "Unknown Platform",
	}
	p := GetPlatform()
	if name, ok := names[p]; ok {
		return name
	}
	return "Unknown Platform"
}
