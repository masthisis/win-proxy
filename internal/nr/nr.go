// Package nr holds cross-cutting constants shared by every internal package.
//
// AI-DEVELOPER NOTES:
//   - Event names are the single source of truth for backend -> frontend
//     communication. The frontend keeps a mirrored list in
//     frontend/src/lib/events.ts. If you add an event here, add it there too
//     and document the payload shape in docs/CONTRACT.md.
//   - Never hardcode ports or magic strings elsewhere; import this package.
package nr

// AppName is the human readable product name.
const AppName = "NeuroRift"

// AppIdentifier is the unique identifier used for single-instance locking and
// registry keys.
const AppIdentifier = "app.neurorift.desktop"

// UpdateRepo is the "owner/name" repository the self-updater (#13) queries
// for new NeuroRift releases.
const UpdateRepo = "masthisis/win-proxy"

// Version is overridden at build time via -ldflags "-X ...".
var Version = "1.8.0"

// Event names emitted on the Wails event bus (backend -> frontend).
const (
        EventCoreStatus      = "core:status"      // payload: engine.Status
        EventCoreDownload    = "core:download"    // payload: core.DownloadProgress
        EventCoreConsole     = "core:console"     // payload: string (core stdout line)
        EventProfilesList    = "profiles:changed" // payload: profile.Summary list (re-fetch via API)
        EventSubProgress     = "sub:progress"     // payload: profile.Progress
        EventLatencyResults  = "latency:results"  // payload: nettest.LatencyBatch
        EventSpeedProgress   = "speed:progress"   // payload: nettest.SpeedProgress
        EventSpeedDone       = "speed:done"       // payload: nettest.SpeedResult
        EventFailover        = "failover:event"   // payload: failover.Event
        EventTraffic         = "net:traffic"      // payload: engine.TrafficTick
        EventConnections     = "net:connections"  // payload: engine.ConnectionsSnapshot
        EventLogsCore        = "core:logs"        // payload: []string (batched lines)
        EventSettingsChanged = "settings:changed" // payload: nil (re-fetch via API)
        EventAppUpdate       = "app:update"       // payload: selfupdate.Status
        EventCoreCrashLoop   = "core:crashloop"   // payload: string detail (#36)
        EventReportsChanged  = "reports:changed"  // payload: nil — pending set changed (#36)
        // EventProxyGuard fires whenever the system-proxy guard acted:
        // re-applied after tampering, kill-switch, or applied-on-start.
        EventProxyGuard = "proxyguard:event" // payload: proxyguard.Status (#47)
        // EventGroupsRefresh is emitted after a config hot-reload succeeded
        // (profile activated / subscription updated): the group tree on the
        // core changed without a lifecycle transition, so views must re-fetch
        // groups and mode (#40).
        EventGroupsRefresh = "groups:refresh" // payload: string reason
        // Tray-originated events (emitted directly from main.go, not the bus).
        EventTraySysProxy = "tray:sysproxy" // payload: bool
        EventTrayMode     = "tray:mode"     // payload: string (rule|global|direct)
)

// Well-known mihomo object names injected by the config generator.
const (
        // GroupSpeedTest is the hidden selector used by the speed-test engine.
        GroupSpeedTest = "⚡ NR SpeedTest"
        // GroupNodeSelect is the primary selector for "nodes" style profiles.
        GroupNodeSelect = "🚀 Node Select"
        // GroupAutoSelect is the url-test group for "nodes" style profiles.
        GroupAutoSelect = "♻️ NR Auto"
        // ListenerSpeedTest is the inbound listener name used by speed tests.
        ListenerSpeedTest = "nr-speedtest"
)

// CoreState enumerates the lifecycle states of the mihomo engine.
type CoreState string

const (
        CoreStopped  CoreState = "stopped"
        CoreStarting CoreState = "starting"
        CoreRunning  CoreState = "running"
        CoreFailed   CoreState = "failed"
        CoreUpdating CoreState = "updating"
)

// Str returns s as a plain string (helper for JSON payloads).
func (c CoreState) String() string { return string(c) }
