// Package exceptions defines custom error types for the MCUB kernel.
// Ported from core/lib/utils/exceptions.py.
// SPDX-License-Identifier: MIT
package exceptions

import (
	"errors"
	"fmt"
)

// CommandConflictError is raised when two modules register the same command.
type CommandConflictError struct {
	Command      string
	ConflictType string // "system" or "user"
	Module       string
}

func (e *CommandConflictError) Error() string {
	if e.Module != "" {
		return fmt.Sprintf("command conflict: %q (type=%s, module=%s)", e.Command, e.ConflictType, e.Module)
	}
	return fmt.Sprintf("command conflict: %q (type=%s)", e.Command, e.ConflictType)
}

// CallInsecure is raised when a module tries to access protected kernel internals.
type CallInsecure struct {
	Name       string
	ModuleName string
}

func (e *CallInsecure) Error() string {
	target := fmt.Sprintf("%q", e.Name)
	if e.ModuleName != "" {
		return fmt.Sprintf("module %q attempted insecure access to %s", e.ModuleName, target)
	}
	return fmt.Sprintf("insecure access to protected core attribute %s", target)
}

// McubTelethonError is raised when telethon-mcub is not installed or unavailable.
type McubTelethonError struct {
	Message string
}

func (e *McubTelethonError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "mcub-telethon is not available"
}

// ModuleLoadError is raised when a module fails to load.
type ModuleLoadError struct {
	ModuleName string
	Cause      error
}

func (e *ModuleLoadError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("failed to load module %q: %v", e.ModuleName, e.Cause)
	}
	return fmt.Sprintf("failed to load module %q", e.ModuleName)
}

func (e *ModuleLoadError) Unwrap() error {
	return e.Cause
}

// ScamModuleDetected is raised by the protection layer when a dangerous
// request is blocked.
type ScamModuleDetected struct {
	Method string
}

func (e *ScamModuleDetected) Error() string {
	return fmt.Sprintf("scam module detected: blocked method %q", e.Method)
}

// IsCommandConflict reports whether err is (or wraps) a *CommandConflictError.
func IsCommandConflict(err error) bool {
	var target *CommandConflictError
	return errors.As(err, &target)
}

// IsCallInsecure reports whether err is (or wraps) a *CallInsecure.
func IsCallInsecure(err error) bool {
	var target *CallInsecure
	return errors.As(err, &target)
}

// IsModuleLoad reports whether err is (or wraps) a *ModuleLoadError.
func IsModuleLoad(err error) bool {
	var target *ModuleLoadError
	return errors.As(err, &target)
}
