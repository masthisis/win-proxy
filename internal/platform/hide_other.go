//go:build !windows

package platform

import "os/exec"

// HideWindow is a no-op on non-Windows platforms (issue #38 is Windows-only:
// no console is allocated for children on unix-likes).
func HideWindow(*exec.Cmd) {}
