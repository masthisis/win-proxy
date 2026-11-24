//go:build !windows

package main

// uwpchild_other.go — non-Windows placeholder for the uwp-exempt child
// (issue #105 is Windows-only; the dispatch in main.go never fires on
// other platforms, this keeps the root package compilable everywhere).

func runUWPExemptChild() int { return 1 }
