package reporting

// state.go — persisted anti-spam state for automatic reports (issue #36):
// per-signature cooldown plus a global daily cap. Manual/test reports bypass
// both (bypass=true) so a user can always file a report right now.

import (
	"encoding/json"
	"os"
	"time"
)

// sendState is the persisted half of the rate limiter.
type sendState struct {
	LastSent map[string]time.Time `json:"lastSent"` // signature -> last filed time
	DayStamp string               `json:"dayStamp"` // "2006-01-02" of DayCount
	DayCount int                  `json:"dayCount"` // automatic reports filed today
}

func loadSendState(path string) *sendState {
	s := &sendState{LastSent: map[string]time.Time{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, s)
	}
	if s.LastSent == nil {
		s.LastSent = map[string]time.Time{}
	}
	return s
}

func (s *sendState) save(path string) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

// allow reports whether a report with the given signature may be filed now.
// bypass (manual/test) skips the cooldown and the daily cap entirely.
func (s *sendState) allow(sig string, now time.Time, bypass bool) (bool, string) {
	if bypass {
		return true, ""
	}
	if last, ok := s.LastSent[sig]; ok {
		if elapsed := now.Sub(last); elapsed < sigCooldown {
			return false, "same signature filed " + elapsed.Truncate(time.Second).String() + " ago (cooldown " + sigCooldown.String() + ")"
		}
	}
	if stamp := now.Format("2006-01-02"); stamp != s.DayStamp {
		return true, "" // new day resets the counter
	}
	if s.DayCount >= dailyAutoCap {
		return false, "daily auto-report cap reached (" + itoa(dailyAutoCap) + "/day)"
	}
	return true, ""
}

// record marks a signature as filed. Bypassed (manual/test) reports do not
// count against the daily cap.
func (s *sendState) record(sig string, now time.Time, bypass bool) {
	if bypass {
		return
	}
	s.LastSent[sig] = now
	// Keep the map bounded: drop entries older than 2× the cooldown.
	for k, t := range s.LastSent {
		if now.Sub(t) > 2*sigCooldown {
			delete(s.LastSent, k)
		}
	}
	if stamp := now.Format("2006-01-02"); stamp != s.DayStamp {
		s.DayStamp = stamp
		s.DayCount = 0
	}
	s.DayCount++
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
