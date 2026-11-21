package nrservice

import (
	"errors"
	"os"
)

// Status is the GUI-facing view of the service (SysService.TunServiceStatus).
type Status struct {
	// Supported is false on platforms without a service implementation
	// (the Settings card hides itself).
	Supported bool `json:"supported"`
	// Installed is true when the SCM registration exists.
	Installed bool `json:"installed"`
	// Running is true when the service state is SERVICE_RUNNING.
	Running bool `json:"running"`
	// Version is the build the RUNNING service reports ("" when not
	// reachable — e.g. started but not accepting yet).
	Version string `json:"version,omitempty"`
	// PID is the service process id (0 when not running).
	PID uint32 `json:"pid"`
}

// facade funcs are implemented per platform: windows_impl.go / stub.go.
//
//   - queryState: SCM lookup → (installed, running, pid)
//   - pingService: pipe round-trip, returns the running build version
//   - doInstall / doUninstall: the ELEVATED child actions (SCM create/delete)
//   - runService: the svc.Handler entry point for `service run`
//   - relaunchElevated: ShellExecute "runas" self-relaunch (the one UAC)

// Query reports the service state. An absent service is a normal state
// (Installed=false), not an error. On platforms without an implementation
// the result is Supported=false with a nil error.
func Query() (Status, error) {
	installed, running, pid, err := queryState()
	if errors.Is(err, ErrUnsupported) {
		return Status{Supported: false}, nil
	}
	if err != nil || !installed {
		return Status{Supported: true}, err
	}
	st := Status{Supported: true, Installed: true, Running: running, PID: pid}
	if running {
		if v, err := pingService(); err == nil {
			st.Version = v
		}
	}
	return st, nil
}

// Install registers the service (requires admin). It self-relaunches with
// the "runas" verb — the user sees exactly one UAC prompt — and returns
// immediately; callers poll Query() for the result.
func Install() error { return relaunchElevated("install") }

// Uninstall removes the service (requires admin, same one-prompt pattern).
func Uninstall() error { return relaunchElevated("uninstall") }

// HandleSubcommand intercepts the service CLI subcommands at the very top
// of main(): "service run" (SCM context, blocks until stop), and the
// elevated one-shot actions "service install" / "service uninstall".
// It returns true when os.Args named one of them (caller returns instead of
// starting the GUI).
func HandleSubcommand(args []string) bool {
	if len(args) < 3 || args[1] != "service" {
		return false
	}
	switch args[2] {
	case "run":
		if err := runService(); err != nil {
			// The SCM records the failure; a service that cannot start
			// must not silently exit 0.
			os.Exit(1)
		}
		return true
	case "install":
		if err := doInstall(); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "uninstall":
		if err := doUninstall(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	default:
		// Unknown subcommand: not ours — fall through to the GUI so
		// unknown args never brick the app.
		return false
	}
	return true
}
