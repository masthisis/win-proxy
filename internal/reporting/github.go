package reporting

// github.go — minimal GitHub REST client for filing issues (#36).
//
// Transport rules (inherited from internal/ghfetch, adapted for the API
// host): api.github.com cannot use release-download mirror prefixes, so the
// chain is "direct client first, then the OS system proxy" — the same
// philosophy that makes downloads work on censored networks during setup.
// Issue creation REQUIRES a token; the caller (Manager.send) resolves it
// from settings at send time and never logs it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/neurorift/neurorift/internal/ghfetch"
)

// ghClient files issues against one repository ("owner/name"). baseURL is
// overridable for tests (httptest fake API server).
type ghClient struct {
	repo    string
	token   string
	baseURL string // defaults to https://api.github.com
}

type issueRequest struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

type issueResponse struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Message string `json:"message"` // error responses
}

// api performs one authenticated GitHub API call and decodes the JSON
// response into out (may be nil). It tries every ghfetch client chain entry
// (direct, then system proxy) and returns the first success.
func (g *ghClient) api(ctx context.Context, method, path string, payload any, out any) error {
	if g == nil || g.repo == "" {
		return fmt.Errorf("reporting: repository not configured")
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	base := g.baseURL
	if base == "" {
		base = "https://api.github.com"
	}
	url := base + path
	var lastErr error
	for _, hc := range ghfetch.Clients(sendTimeout) {
		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if g.token != "" {
			req.Header.Set("Authorization", "Bearer "+g.token)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("github %s %s: HTTP %d: %s", method, path, resp.StatusCode, apiMessage(data))
			continue
		}
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("github response decode: %w", err)
			}
		}
		return nil
	}
	return lastErr
}

// apiMessage extracts GitHub's {"message": "..."} from an error body.
func apiMessage(data []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &m); err == nil && m.Message != "" {
		return m.Message
	}
	return strings.TrimSpace(string(data))
}

// CreateIssue files the issue and returns (number, html_url, error).
func (g *ghClient) CreateIssue(ctx context.Context, title, body string, labels []string) (int, string, error) {
	// Labels are best-effort: fine-grained PATs without administration
	// permission cannot create labels; GitHub silently drops unknown labels
	// on issue creation, so never fail the report over them.
	for _, l := range labels {
		var existing issueResponse
		_ = g.api(ctx, http.MethodPost, "/repos/"+g.repo+"/labels",
			map[string]string{"name": l}, &existing)
	}
	var out issueResponse
	err := g.api(ctx, http.MethodPost, "/repos/"+g.repo+"/issues",
		issueRequest{Title: title, Body: body, Labels: labels}, &out)
	if err != nil {
		return 0, "", err
	}
	if out.Number == 0 {
		return 0, "", fmt.Errorf("github issue create returned no number")
	}
	return out.Number, out.HTMLURL, nil
}

// CreateComment appends an overflow continuation to a filed issue.
func (g *ghClient) CreateComment(ctx context.Context, number int, body string) error {
	var out issueResponse
	return g.api(ctx, http.MethodPost,
		fmt.Sprintf("/repos/%s/issues/%d/comments", g.repo, number),
		map[string]string{"body": body}, &out)
}
