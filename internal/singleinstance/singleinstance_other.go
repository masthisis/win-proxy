//go:build !windows

// singleinstance_other.go — no-op fallback for non-Windows builds so the
// package API compiles everywhere. Desktop parity builds (#19) will replace
// this with their platform-native implementations.

package singleinstance

import "log/slog"

// Acquire is a no-op outside Windows: every launch is treated as first.
func Acquire() error { return nil }

// ListenOnMainThread is a no-op outside Windows.
func ListenOnMainThread(_ *slog.Logger, _ func()) {}

// Release is a no-op outside Windows.
func Release() {}
