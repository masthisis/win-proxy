//go:build !windows

package nrservice

// Service registration identity kept identical across platforms so callers
// can reference it without build tags.
const (
	SvcName        = "NeuroRiftService"
	SvcDisplayName = "NeuroRift Service"
	SvcDescription = "Runs the NeuroRift proxy core with elevated privileges (TUN mode) without per-launch UAC prompts. Installed and controlled by the NeuroRift app."
)

func queryState() (bool, bool, uint32, error) {
	return false, false, 0, ErrUnsupported
}

func pingService() (string, error) { return "", ErrUnsupported }

func doInstall() error   { return ErrUnsupported }
func doUninstall() error { return ErrUnsupported }

func runService() error { return ErrUnsupported }

func relaunchElevated(string) error { return ErrUnsupported }

// --- stage-2 client API stubs -------------------------------------------------

// Session mirrors the Windows type so callers compile on every platform.
type Session struct{}

func StartCoreSession(string, string, []string) (*Session, error) { return nil, ErrUnsupported }

func (s *Session) Close() error { return nil }

func StopCore() error { return ErrUnsupported }

// CoreInfo mirrors the Windows type.
type CoreInfo struct {
	State     string
	PID       uint32
	LastError string
}

func CoreStatus() (CoreInfo, error) { return CoreInfo{}, ErrUnsupported }
