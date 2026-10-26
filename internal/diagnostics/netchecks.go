package diagnostics

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"pakka/internal/evidence"
)

// checkSource marks results collected live by the harness.
const checkSource = "harness"

func result(id string, out evidence.Outcome, summary string, ev map[string]any, start time.Time) evidence.Result {
	return evidence.Result{
		CheckID:    id,
		Timestamp:  time.Now().UTC(),
		Source:     checkSource,
		Outcome:    out,
		Summary:    summary,
		Evidence:   ev,
		DurationMs: time.Since(start).Milliseconds(),
	}
}

func skipped(id, summary string) evidence.Result {
	return evidence.Result{
		CheckID:   id,
		Timestamp: time.Now().UTC(),
		Source:    checkSource,
		Outcome:   evidence.OutcomeSkipped,
		Summary:   summary,
		Evidence:  map[string]any{},
	}
}

// DNSCheck resolves a configured host.
type DNSCheck struct{ host string }

// NewDNSCheck builds a resolve check; empty host reports skipped.
func NewDNSCheck(host string) *DNSCheck { return &DNSCheck{host: host} }

// ID implements Check.
func (c *DNSCheck) ID() string { return "dns.resolve" }

// Run implements Check.
func (c *DNSCheck) Run(ctx context.Context) evidence.Result {
	start := time.Now()
	if c.host == "" {
		return skipped(c.ID(), "no DNS target configured")
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, c.host)
	if err != nil {
		return result(c.ID(), evidence.OutcomeFail,
			fmt.Sprintf("resolve %s: %s", c.host, err),
			map[string]any{"host": c.host}, start)
	}
	return result(c.ID(), evidence.OutcomePass,
		fmt.Sprintf("resolve %s: %d addr(s)", c.host, len(addrs)),
		map[string]any{"host": c.host, "addrs": addrs}, start)
}

// TCPCheck dials a configured address.
type TCPCheck struct{ addr string }

// NewTCPCheck builds a connect check; empty addr reports skipped.
func NewTCPCheck(addr string) *TCPCheck { return &TCPCheck{addr: addr} }

// ID implements Check.
func (c *TCPCheck) ID() string { return "tcp.connect" }

// Run implements Check.
func (c *TCPCheck) Run(ctx context.Context) evidence.Result {
	start := time.Now()
	if c.addr == "" {
		return skipped(c.ID(), "no TCP target configured")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return result(c.ID(), evidence.OutcomeFail,
			fmt.Sprintf("dial %s: %s", c.addr, err),
			map[string]any{"addr": c.addr}, start)
	}
	_ = conn.Close()
	return result(c.ID(), evidence.OutcomePass,
		fmt.Sprintf("dial %s: connected", c.addr),
		map[string]any{"addr": c.addr}, start)
}

// HTTPCheck fetches a configured health URL.
type HTTPCheck struct{ url string }

// NewHTTPCheck builds a health check; empty URL reports skipped.
func NewHTTPCheck(url string) *HTTPCheck { return &HTTPCheck{url: url} }

// ID implements Check.
func (c *HTTPCheck) ID() string { return "http.health" }

// Run implements Check.
func (c *HTTPCheck) Run(ctx context.Context) evidence.Result {
	start := time.Now()
	if c.url == "" {
		return skipped(c.ID(), "no health URL configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return result(c.ID(), evidence.OutcomeError,
			fmt.Sprintf("bad URL: %s", err), map[string]any{}, start)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return result(c.ID(), evidence.OutcomeFail,
			fmt.Sprintf("GET %s: %s", c.url, err),
			map[string]any{"url": c.url}, start)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	ev := map[string]any{"url": c.url, "status": resp.StatusCode}
	if resp.StatusCode/100 != 2 {
		return result(c.ID(), evidence.OutcomeFail,
			fmt.Sprintf("GET %s: status %d", c.url, resp.StatusCode), ev, start)
	}
	return result(c.ID(), evidence.OutcomePass,
		fmt.Sprintf("GET %s: status %d", c.url, resp.StatusCode), ev, start)
}
