//go:build windows

package main

// uwpchild.go — the elevated one-shot child for issue #105:
// `neurorift.exe uwp-exempt` adds every installed AppContainer to the
// Windows loopback exemption list (UWP apps + system proxy), writes a
// result marker into the data root and exits. Dispatched from main()
// BEFORE the GUI starts — the same pre-GUI pattern the TUN service
// subcommands use.

import (
	"path/filepath"
	"time"

	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/platform"
	"github.com/neurorift/neurorift/internal/xjson"
)

// uwpExemptMarker is the persisted result of one exemption run.
type uwpExemptMarker struct {
	At       string `json:"at"`
	Elevated bool   `json:"elevated"`
	Families int    `json:"families"`
	Error    string `json:"error,omitempty"`
}

// runUWPExemptChild performs the loopback exemption from the elevated
// child process and returns the exit code.
func runUWPExemptChild() int {
	res, err := platform.ExemptUWP()
	m := uwpExemptMarker{
		At:       time.Now().Format(time.RFC3339),
		Elevated: res.Elevated,
		Families: res.Families,
	}
	if err != nil {
		m.Error = err.Error()
	}
	writeUWPExemptMarker(m)
	if err != nil {
		return 1
	}
	return 0
}

// writeUWPExemptMarker persists the result best-effort (GUI builds have no
// console; the marker makes the run inspectable from the data folder).
func writeUWPExemptMarker(m uwpExemptMarker) {
	p, err := appmeta.Resolve()
	if err != nil {
		return
	}
	_ = xjson.NewStore[uwpExemptMarker](filepath.Join(p.Root, "uwp-exempt-last.json")).Save(m)
}
