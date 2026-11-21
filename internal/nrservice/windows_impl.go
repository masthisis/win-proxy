//go:build windows

package nrservice

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Service registration identity. The display name and description show up
// in services.msc — keep them human.
const (
	SvcName        = "NeuroRiftService"
	SvcDisplayName = "NeuroRift Service"
	SvcDescription = "Runs the NeuroRift proxy core with elevated privileges (TUN mode) without per-launch UAC prompts. Installed and controlled by the NeuroRift app."
)

// --- pipe -------------------------------------------------------------------

// listenPipe opens the service-side named pipe. The SDDL keeps the surface
// local-only: SYSTEM + interactive sessions, nothing inherited.
func listenPipe() (net.Listener, error) {
	return winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: PipeSDDL,
		MessageMode:        false,
		InputBufferSize:    4096,
		OutputBufferSize:   4096,
	})
}

// dialPipe connects to the service pipe. DialPipe retries internally until
// the timeout elapses (the pipe may not exist for a moment after the
// service reports RUNNING).
func dialPipe(timeout time.Duration) (net.Conn, error) {
	conn, err := winio.DialPipe(PipeName, &timeout)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", PipeName, err)
	}
	return conn, nil
}

// pingService does one ping round-trip and returns the reported version.
func pingService() (string, error) {
	conn, err := dialPipe(2 * time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := WriteFrame(conn, Request{V: ProtoVersion, Op: OpPing}); err != nil {
		return "", err
	}
	var resp Response
	if err := ReadFrame(conn, &resp); err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("ping rejected: %s", resp.Error)
	}
	if v, ok := resp.Result["version"].(string); ok {
		return v, nil
	}
	return "", nil
}

// --- service handler ---------------------------------------------------------

// nrService implements svc.Handler: it serves the pipe until SCM stop.
type nrService struct{}

func (m *nrService) Execute(_ []string, r <-chan svc.ChangeRequest, w chan<- svc.Status) (bool, uint32) {
	const acceptExit = uint32(1)
	w <- svc.Status{State: svc.StartPending, WaitHint: 15000}

	ln, err := listenPipe()
	if err != nil {
		// Non-zero exit code: services.msc shows the failure instead of a
		// service that "started" and does nothing.
		return false, acceptExit
	}
	w <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	serveCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		serve(serveCtx, ln)
	}()

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				w <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				w <- svc.Status{State: svc.StopPending, WaitHint: 5000}
				cancel()
				_ = ln.Close()
				select {
				case <-done:
				case <-time.After(4 * time.Second):
					// Leak the accept goroutine rather than fail the stop;
					// process exit reaps everything.
				}
				return false, 0
			default:
				w <- c.CurrentStatus
			}
		}
	}
}

// serve accepts pipe clients; each connection carries exactly one
// request/response pair. Serve errors never take the service down — the
// listener loop keeps accepting (a wedged client must not wedge TUN).
func serve(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		go handleConn(ctx, conn)
	}
}

func handleConn(_ context.Context, conn net.Conn) {
	defer conn.Close()
	// A stuck client holds one goroutine for at most 5 seconds.
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var req Request
	if err := ReadFrame(conn, &req); err != nil {
		return
	}
	resp := HandleRequest(req)
	_ = WriteFrame(conn, resp)
}

// runService is the `service run` entry point (SCM context only).
func runService() error {
	return svc.Run(SvcName, &nrService{})
}

// --- SCM control (elevated child path) ----------------------------------------

// queryState reports (installed, running, pid). SCM errors are surfaced to
// the caller; a missing service is installed=false, nil error.
func queryState() (bool, bool, uint32, error) {
	scm, err := mgr.Connect()
	if err != nil {
		return false, false, 0, err
	}
	defer scm.Disconnect()
	s, err := scm.OpenService(SvcName)
	if err != nil {
		return false, false, 0, nil // not installed — normal state
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return true, false, 0, err
	}
	running := st.State == svc.Running
	pid := uint32(0)
	if running {
		pid = st.ProcessId
	}
	return true, running, pid, nil
}

// doInstall creates and starts the service. Runs in the ELEVATED child
// (`neurorift.exe service install`).
func doInstall() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	scm, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("open service control manager: %w", err)
	}
	defer scm.Disconnect()

	// Re-install = delete then create (covers binary moves/updates).
	if s, err := scm.OpenService(SvcName); err == nil {
		_, _ = s.Control(svc.Stop)
		_ = s.Delete()
		_ = s.Close()
		waitGone(scm)
	}

	image := fmt.Sprintf(`"%s" service run`, exePath)
	s, err := scm.CreateService(SvcName, image, mgr.Config{
		DisplayName: SvcDisplayName,
		Description: SvcDescription,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()
	// Delayed auto-start: boot is not slowed, the service is simply there
	// whenever the app needs it — which is the point (no prompts at use
	// time). Best effort: mgr does not expose the flag, so write the
	// service registry value directly (we are elevated here).
	setDelayedAutoStart()
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

// doUninstall stops (best effort) and deletes the service. ELEVATED child.
func doUninstall() error {
	scm, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("open service control manager: %w", err)
	}
	defer scm.Disconnect()
	s, err := scm.OpenService(SvcName)
	if err != nil {
		return nil // already gone — success for the caller
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
		waitStopped(s)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	return nil
}

// setDelayedAutoStart writes the DelayedAutoStart flag for the just-created
// service. Best effort: failing this only means the service starts during
// boot instead of a moment later.
func setDelayedAutoStart() {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+SvcName, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	_ = key.SetDWordValue("DelayedAutoStart", 1)
}

// waitGone polls until OpenService fails (deleted), bounded ~8s.
func waitGone(scm *mgr.Mgr) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if s, err := scm.OpenService(SvcName); err != nil {
			return
		} else {
			_ = s.Close()
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// waitStopped polls until the service leaves the running state, bounded ~8s.
func waitStopped(s *mgr.Service) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := s.Query(); err == nil && st.State == svc.Stopped {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// --- elevation ----------------------------------------------------------------

// relaunchElevated starts `neurorift.exe service <action>` through the
// shell with the "runas" verb. This is the ONLY UAC prompt in the design;
// the call is fire-and-forget — the GUI polls Query() for the outcome.
func relaunchElevated(action string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	argv, err := windows.UTF16PtrFromString("service " + action)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, argv, nil, windows.SW_SHOWNORMAL)
}
