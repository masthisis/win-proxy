// Package appmeta resolves the on-disk layout of the application.
//
// Layout (created on first run):
//
//	<configDir>/NeuroRift/
//	  settings.json        app settings (internal/settings)
//	  profiles.json        profile registry (internal/profile)
//	  speedhistory.json    persisted speed-test results (internal/nettest)
//	  logs/neurorift.log   rotating application log
//	  reports/             captured problem reports (#36): pending/ + sent/
//	  providers/           materialized mihomo proxy-provider files
//	  clash/<id>.yaml      raw Clash YAML for "clash" kind profiles
//	  core/                mihomo binary, config.yaml, cache.db, geodata
//
// The config dir is %APPDATA% on Windows, ~/Library/Application Support on
// macOS and $XDG_CONFIG_HOME on Linux, resolved via os.UserConfigDir().
//
// AI-DEVELOPER NOTES:
//   - Paths.Resolve is the ONLY place that knows the layout; new artifacts
//     must be added here and documented in this header.
package appmeta

import (
	"os"
	"path/filepath"

	"github.com/neurorift/neurorift/internal/nr"
)

// Paths is the resolved filesystem layout of the application data.
type Paths struct {
	// Root is the application data directory.
	Root string
	// Settings is the settings JSON path.
	Settings string
	// Profiles is the profile registry JSON path.
	Profiles string
	// SpeedHistory is the speed-test history JSON path.
	SpeedHistory string
	// LogsDir holds rotated application logs.
	LogsDir string
	// ProvidersDir holds materialized proxy-provider files.
	ProvidersDir string
	// ClashDir holds raw Clash YAML profile files.
	ClashDir string
	// CoreDir holds the mihomo binary, runtime config and geodata.
	CoreDir string
	// ReportsDir holds captured problem reports (#36): pending/ + sent/.
	ReportsDir string
}

func join(root string, parts ...string) string {
	return filepath.Join(append([]string{root}, parts...)...)
}

// Resolve computes the layout for the default user config directory.
func Resolve() (*Paths, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		base, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	root := join(base, nr.AppName)
	return ResolveAt(root), nil
}

// ResolveAt computes the layout for an explicit root. Useful for tests.
func ResolveAt(root string) *Paths {
	return &Paths{
		Root:         root,
		Settings:     join(root, "settings.json"),
		Profiles:     join(root, "profiles.json"),
		SpeedHistory: join(root, "speedhistory.json"),
		LogsDir:      join(root, "logs"),
		ProvidersDir: join(root, "providers"),
		ClashDir:     join(root, "clash"),
		CoreDir:      join(root, "core"),
		ReportsDir:   join(root, "reports"),
	}
}

// Ensure creates the directory skeleton. Safe to call repeatedly.
func (p *Paths) Ensure() error {
	for _, dir := range []string{p.Root, p.LogsDir, p.ProvidersDir, p.ClashDir, p.CoreDir, p.ReportsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// CoreBinaryName returns the mihomo executable file name for the current GOOS.
func CoreBinaryName() string {
	if os.PathSeparator == '\\' {
		return "mihomo.exe"
	}
	return "mihomo"
}

// CoreBinaryPath returns the absolute path of the (optionally compatible
// variant) mihomo binary inside CoreDir.
func (p *Paths) CoreBinaryPath(compatible bool) string {
	name := CoreBinaryName()
	if compatible {
		name = "mihomo-compatible" + binaryExt()
	}
	return join(p.CoreDir, name)
}

// CoreConfigPath is the runtime config consumed by mihomo (-f).
func (p *Paths) CoreConfigPath() string { return join(p.CoreDir, "config.yaml") }

func binaryExt() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}
