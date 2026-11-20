package main

// service_reporting.go — problem reports exposed to the frontend (#36).
//
// All methods are thin: capture/queue/send logic lives in
// internal/reporting. The UI uses:
//   - Settings → Problem reporting: token + enable toggle (settings doc),
//     pending list, "Send test report".
//   - About → "Report a problem" (manual capture, includes log tail).

import (
	"fmt"

	"github.com/neurorift/neurorift/internal/reporting"
)

// ReportService manages problem reports.
type ReportService struct{ c *Container }

// PendingInfo is the UI-facing summary of one pending report.
type PendingInfo struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Detail    string `json:"detail"`
	Time      string `json:"time"`
	Version   string `json:"version"`
	Signature string `json:"signature"`
}

// TokenConfigured reports whether a GitHub token is set (UI hint).
func (s *ReportService) TokenConfigured() bool { return s.c.Reports.TokenConfigured() }

// Pending lists captured-but-not-yet-filed reports.
func (s *ReportService) Pending() []PendingInfo {
	caps := s.c.Reports.Pending()
	out := make([]PendingInfo, 0, len(caps))
	for _, c := range caps {
		out = append(out, PendingInfo{
			ID:        c.ID,
			Type:      string(c.Type),
			Detail:    c.Detail,
			Time:      c.Time.Format("2006-01-02 15:04:05"),
			Version:   c.Version,
			Signature: c.Signature,
		})
	}
	return out
}

// Send files one pending report now and returns the issue URL.
func (s *ReportService) Send(id string) (string, error) {
	if !s.c.Reports.TokenConfigured() {
		return "", fmt.Errorf("no GitHub token configured — add one under Settings → Problem reporting")
	}
	url, err := s.c.Reports.SendPending(id)
	if err != nil {
		return "", err
	}
	return openReportedIssue(url), nil
}

// SendAll files every pending report now; returns how many were filed.
func (s *ReportService) SendAll() (int, error) {
	if !s.c.Reports.TokenConfigured() {
		return 0, fmt.Errorf("no GitHub token configured — add one under Settings → Problem reporting")
	}
	return s.c.Reports.SendAllPending()
}

// Delete discards one pending report.
func (s *ReportService) Delete(id string) error { return s.c.Reports.DeletePending(id) }

// SendTestReport files a type=test issue synchronously; returns the issue
// URL so the UI can show/link it. Errors explain what is missing (token,
// permissions, network).
func (s *ReportService) SendTestReport() (string, error) {
	if !s.c.Reports.TokenConfigured() {
		return "", fmt.Errorf("no GitHub token configured — add one under Settings → Problem reporting")
	}
	url, err := s.c.Reports.CaptureForResult(reporting.TypeTest,
		"Test report from the NeuroRift settings screen — if you can read this, automatic problem reporting works end-to-end.",
		"", nil)
	if err != nil {
		return "", err
	}
	return openReportedIssue(url), nil
}

// ReportProblem captures a manual report from the About page (always
// allowed, even with automatic reporting disabled). Without a token it
// lands in the pending queue; with one it is filed immediately.
func (s *ReportService) ReportProblem() (string, error) {
	url, err := s.c.Reports.CaptureForResult(reporting.TypeManual,
		"Manually reported problem from the NeuroRift About page (user did not attach a description — see the log tail and recent state).",
		"", nil)
	if err != nil {
		return "", err
	}
	return openReportedIssue(url), nil
}

// OpenFolder opens the reports directory in the OS file manager.
func (s *ReportService) OpenFolder() error { return openPath(s.c.Paths.ReportsDir) }

// openReportedIssue normalizes a GitHub issue URL for the UI (opens the
// filed issue in the browser via the standard bridge).
func openReportedIssue(url string) string {
	if url != "" {
		_ = openURL(url)
	}
	return url
}
