package profile

import (
        "context"
        "io"
        "log/slog"
        "os"
        "path/filepath"
        "strings"
        "testing"

        "github.com/neurorift/neurorift/internal/bus"
        "github.com/neurorift/neurorift/internal/settings"
)

// newMgrForTest builds a real profile manager (bus + settings wired) whose
// registry holds one clash profile materialized on disk.
func newMgrForTest(t *testing.T) *Manager {
        t.Helper()
        dir := t.TempDir()
        providers := filepath.Join(dir, "providers")
        clashDir := filepath.Join(dir, "clash")
        for _, d := range []string{providers, clashDir} {
                if err := os.MkdirAll(d, 0o755); err != nil {
                        t.Fatal(err)
                }
        }
        regPath := filepath.Join(dir, "profiles.json")
        settingsPath := filepath.Join(dir, "settings.json")

        log := slog.New(slog.NewTextHandler(io.Discard, nil))
        b := bus.New(log)
        set := settings.NewManager(settingsPath, b, log)

        const id = "c1"
        yaml := "proxies:\n  - name: u1\n    type: ss\n    server: s\n    port: 1\n    cipher: aes-128-gcm\n    password: p\nrules:\n  - MATCH,DIRECT\n"
        if err := os.WriteFile(filepath.Join(clashDir, id+".yaml"), []byte(yaml), 0o644); err != nil {
                t.Fatal(err)
        }
        reg := `{"profiles":[{"id":"` + id + `","name":"Clashy","kind":"clash","source":"clipboard"}],"activeId":"` + id + `"}`
        if err := os.WriteFile(regPath, []byte(reg), 0o644); err != nil {
                t.Fatal(err)
        }
        return NewManager(regPath, providers, clashDir, b, set, log)
}

func TestLintYAML_Valid(t *testing.T) {
        doc := "proxies:\n  - name: a\n    type: ss\n    server: s\n    port: 1\n  - name: b\n    type: trojan\n    server: s2\n    port: 2\nrules:\n  - MATCH,DIRECT\n"
        if issues := LintYAML(doc); len(issues) != 0 {
                t.Fatalf("expected clean lint, got %+v", issues)
        }
}

func TestLintYAML_SyntaxErrorCarriesLine(t *testing.T) {
        bad := "proxies:\n  - name: a\n    type: ss\n  bad_indent: [\n"
        issues := LintYAML(bad)
        if len(issues) == 0 || !issues[0].Fatal {
                t.Fatalf("expected fatal syntax issue, got %+v", issues)
        }
        if issues[0].Line < 1 || issues[0].Message == "" {
                t.Errorf("syntax issue should carry a line and message, got %+v", issues[0])
        }
}

func TestLintYAML_Semantics(t *testing.T) {
        cases := []struct {
                name string
                doc  string
                want string
        }{
                {"duplicate proxy names", "proxies:\n  - name: a\n    type: ss\n  - name: a\n    type: ss\n", "duplicate proxy name"},
                {"missing type", "proxies:\n  - name: a\n    server: s\n", "missing a \"type\""},
                {"no proxies at all", "rules:\n  - MATCH,DIRECT\n", "neither proxies nor proxy-providers"},
                {"root not mapping", "- a\n- b\n", "must be a mapping"},
                {"duplicate top-level keys", "port: 1\nport: 2\nproxies:\n  - name: a\n    type: ss\n", "duplicate top-level key"},
        }
        for _, tc := range cases {
                t.Run(tc.name, func(t *testing.T) {
                        issues := LintYAML(tc.doc)
                        found := false
                        for _, is := range issues {
                                if strings.Contains(is.Message, tc.want) {
                                        found = true
                                }
                        }
                        if !found {
                                t.Fatalf("expected an issue containing %q, got %+v", tc.want, issues)
                        }
                })
        }
}

func TestGetRawAndUpdateRaw(t *testing.T) {
        m := newMgrForTest(t)
        const id = "c1"

        raw, err := m.GetRaw(id)
        if err != nil {
                t.Fatal(err)
        }
        if !strings.Contains(raw, "u1") {
                t.Fatalf("GetRaw returned unexpected content:\n%s", raw)
        }

        // Invalid content must NOT be written.
        res, err := m.UpdateRaw(context.Background(), id, "rules:\n  - MATCH,DIRECT\n")
        if err != nil {
                t.Fatal(err)
        }
        if res.OK || len(res.Issues) == 0 {
                t.Fatalf("expected lint issues blocking the save, got %+v", res)
        }
        after, _ := m.GetRaw(id)
        if after != raw {
                t.Fatal("a failed validation must not modify the file")
        }

        // Valid edit is saved atomically.
        edited := strings.Replace(raw, "name: u1", "name: renamed", 1)
        res, err = m.UpdateRaw(context.Background(), id, edited)
        if err != nil {
                t.Fatal(err)
        }
        if !res.OK {
                t.Fatalf("valid edit rejected: %+v", res)
        }
        saved, _ := m.GetRaw(id)
        if !strings.Contains(saved, "renamed") {
                t.Fatalf("edit not persisted:\n%s", saved)
        }
        if _, err := os.Stat(m.ClashPathFor(id) + ".tmp"); !os.IsNotExist(err) {
                t.Fatal("temp file leaked next to the profile")
        }

        // nodes profiles are read-only via this API.
        _, err = m.UpdateRaw(context.Background(), "nodes-x", "proxies: []")
        if err == nil {
                t.Fatal("unknown profile must error")
        }
}
