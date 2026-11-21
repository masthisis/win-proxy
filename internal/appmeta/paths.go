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
// PORTABLE MODE (#18): when a portable.flag file exists next to the running
// executable, ALL state resolves to <exe-dir>/data/... instead of the
// per-user config dir. The mode is decided once here — before any store
// opens — so every consumer (settings, profiles, logs, core, reports)
// switches atomically. Migration from an existing %APPDATA% install is
// NEVER automatic: the Settings UI offers an explicit import action
// (ImportLegacyInto), which refuses to touch an already-initialized
// portable data dir.
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

// PortableFlag is the marker file next to the exe that activates portable
// mode. Empty (or comment-only) content is fine — presence is what counts.
const PortableFlag = "portable.flag"

// PortableDataDirName is the directory (next to the exe) that holds every
// artifact in portable mode.
const PortableDataDirName = "data"

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
	// Portable reports whether all state lives under <exe-dir>/data
	// (portable.flag next to the executable) instead of the per-user
	// config dir. Decided once at startup, before any store opens (#18).
	Portable bool
	// ExeDir is the directory containing the running executable ("" when
	// os.Executable fails — never expected on Windows). In portable mode
	// ExeDir is Root's parent.
	ExeDir string
}

func join(root string, parts ...string) string {
	return filepath.Join(append([]string{root}, parts...)...)
}

// Resolve computes the layout for the running executable's environment.
// Portable mode wins when <exe-dir>/portable.flag exists (#18).
func Resolve() (*Paths, error) {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	return ResolveExe(exeDir)
}

// ResolveExe computes the layout given the directory of the running
// executable ("" disables portable detection). Split from Resolve so tests
// can exercise both modes without touching the real executable location.
func ResolveExe(exeDir string) (*Paths, error) {
	if exeDir != "" {
		if _, err := os.Stat(filepath.Join(exeDir, PortableFlag)); err == nil {
			p := ResolveAt(filepath.Join(exeDir, PortableDataDirName))
			p.Portable = true
			p.ExeDir = exeDir
			return p, nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	p := ResolveAt(join(base, nr.AppName))
	p.ExeDir = exeDir
	return p, nil
}

// LegacyRoot returns the per-user config root (%APPDATA%\NeuroRift on
// Windows) or "" when the OS does not expose one. Portable mode reads it
// only through the EXPLICIT Settings import action (#18) — never silently.
func LegacyRoot() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return join(base, nr.AppName)
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
