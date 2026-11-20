package nettest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/neurorift/neurorift/internal/nr"
)

// speedClient builds an HTTP client whose traffic is routed through the
// dedicated speed-test listener.
func (m *Manager) speedClient() *http.Client {
	s := m.Settings.Get()
	proxy := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", s.Ports.SpeedTest)}
	return &http.Client{
		Timeout: time.Duration(s.Test.SpeedMaxSeconds+5) * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(proxy),
			MaxIdleConns:          16,
			IdleConnTimeout:       15 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
}

// SpeedTest measures download (and optionally upload) throughput of one
// node. Only one test may run at a time; call CancelSpeedTest to abort.
func (m *Manager) SpeedTest(parent context.Context, node string) (*SpeedResult, error) {
	m.speedMu.Lock()
	defer m.speedMu.Unlock()
	if m.speedCancel != nil {
		return nil, fmt.Errorf("a speed test is already running")
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancelMu.Lock()
	m.speedCancel = cancel
	m.cancelMu.Unlock()
	defer func() {
		m.cancelMu.Lock()
		m.speedCancel = nil
		m.cancelMu.Unlock()
	}()

	api := m.Engine.API()
	if api == nil {
		return nil, fmt.Errorf("engine not running")
	}

	// Point the hidden speed-test selector at the target node.
	if err := api.SelectProxy(ctx, nr.GroupSpeedTest, node); err != nil {
		return nil, fmt.Errorf("select node for test: %w", err)
	}

	s := m.Settings.Get()
	client := m.speedClient()
	result := &SpeedResult{Node: node, At: time.Now()}
	start := time.Now()

	// ---- download phase ----
	downBytes, downMS, err := m.transfer(ctx, client, s.Test.SpeedURL, s.Test.SpeedStreams,
		s.Test.SpeedMaxSeconds, int64(s.Test.SpeedMaxMB)<<20, "download", node)
	result.DownBytes = downBytes
	if err != nil && downBytes == 0 {
		result.Error = fmt.Sprintf("download: %v", err)
		result.DurationMS = time.Since(start).Milliseconds()
		m.Bus.Emit(nr.EventSpeedDone, result)
		return result, nil // partial result, not a hard failure
	}
	if downMS > 0 {
		result.DownloadMbps = mbps(downBytes, downMS)
	}

	// ---- upload phase ----
	if s.Test.UploadTest && result.Error == "" {
		upBytes, upMS, err := m.transfer(ctx, client, s.Test.SpeedUploadURL, 1,
			min(s.Test.SpeedMaxSeconds, 10), int64(s.Test.SpeedMaxMB)<<20, "upload", node)
		result.UpBytes = upBytes
		if err != nil && upBytes == 0 {
			result.Error = fmt.Sprintf("upload: %v", err)
		} else if upMS > 0 {
			result.UploadMbps = mbps(upBytes, upMS)
		}
	}

	result.DurationMS = time.Since(start).Milliseconds()
	m.Bus.Emit(nr.EventSpeedDone, result)
	return result, nil
}

// SpeedTestQueue runs speed tests sequentially over nodes, emitting
// progress/done events per node. It returns the first hard failure.
func (m *Manager) SpeedTestQueue(ctx context.Context, nodes []string) error {
	for _, n := range nodes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := m.SpeedTest(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// transfer runs a parallel transfer measurement. It reads (GET) or writes
// (POST) data through the proxy until the deadline, byte cap or context
// cancellation, emitting progress along the way.
func (m *Manager) transfer(ctx context.Context, client *http.Client, rawURL string, streams, maxSeconds int, maxBytes int64, phase, node string) (int64, int64, error) {
	if streams < 1 {
		streams = 1
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(maxSeconds)*time.Second)
	defer cancel()

	var total atomic.Int64
	upload := phase == "upload"
	var body []byte
	if upload {
		// 4 MiB of entropy-ish data per stream, reused.
		body = bytes.Repeat([]byte{0xA5, 0x5A, 0x37, 0x73}, 1<<20)
	}

	start := time.Now()
	done := make(chan error, streams)
	for i := 0; i < streams; i++ {
		go func() {
			err := m.oneStream(ctx, client, rawURL, upload, body, maxBytes/int64(streams), &total)
			done <- err
		}()
	}

	// Progress reporter.
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		last := int64(0)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				cur := total.Load()
				el := time.Since(start).Milliseconds()
				if el <= 0 {
					continue
				}
				m.Bus.Emit(nr.EventSpeedProgress, SpeedProgress{
					Node: node, Phase: phase, ElapsedMS: el,
					MBps: mbps(cur-last, 500), Bytes: cur,
				})
				last = cur
			}
		}
	}()

	var firstErr error
	for i := 0; i < streams; i++ {
		if err := <-done; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	cancel()
	<-progressDone

	elapsed := time.Since(start).Milliseconds()
	if total.Load() == 0 && firstErr != nil {
		return 0, elapsed, firstErr
	}
	return total.Load(), elapsed, nil
}

// oneStream performs a single GET (discard body) or POST upload.
func (m *Manager) oneStream(ctx context.Context, client *http.Client, rawURL string, upload bool, body []byte, cap int64, total *atomic.Int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if upload {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.ContentLength = int64(len(body))
	}
	resp, err := client.Do(req)
	if err != nil {
		// Deadline reached mid-transfer is a normal completion.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer resp.Body.Close()

	if upload {
		// Body fully sent when Do returns with a buffer? For chunked
		// requests Do returns after the body is transmitted.
		total.Add(int64(len(body)))
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	buf := make([]byte, 64*1024)
	var read int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			read += int64(n)
			total.Add(int64(n))
			if cap > 0 && total.Load() >= cap {
				return nil
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return rerr
		}
	}
}

// mbps converts bytes/milliseconds to megabits per second.
func mbps(bytes int64, ms int64) float64 {
	if ms <= 0 {
		return 0
	}
	return float64(bytes) * 8 / float64(ms) / 1000 // bytes*8/(ms*1e-3)/1e6
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
