package eshop

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// CommandTrigger runs the eShop sync pipeline (hbstore-tools on a host with
// the packaging toolchain and console keys) as a shell command. Runs are
// single-flight with a cooldown: overlapping requests are refused.
type CommandTrigger struct {
	command  string
	timeout  time.Duration
	cooldown time.Duration

	mu        sync.Mutex
	running   bool
	lastStart time.Time
	lastEnd   time.Time
	lastOK    bool
	lastInfo  string
}

// NewCommandTrigger builds the trigger; an empty command disables it
// (the caller keeps a nil Trigger instead).
func NewCommandTrigger(command string, timeout, cooldown time.Duration) *CommandTrigger {
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	if cooldown <= 0 {
		cooldown = 6 * time.Hour
	}
	return &CommandTrigger{command: command, timeout: timeout, cooldown: cooldown}
}

// Request implements Trigger: it starts the sync command in the background
// and posts the finish notice through post.
func (t *CommandTrigger) Request(reason string, post func(msg string)) (bool, string) {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return false, "a sync is already running — it covers this release"
	}
	if !t.lastStart.IsZero() {
		if wait := t.cooldown - time.Since(t.lastStart); wait > 0 {
			t.mu.Unlock()
			return false, fmt.Sprintf("sync cooling down (%s left) — the daily sync will pick it up", wait.Round(time.Minute))
		}
	}
	t.running = true
	t.lastStart = time.Now()
	t.mu.Unlock()
	go t.run(reason, post)
	return true, "sync started"
}

// Status implements Trigger.
func (t *CommandTrigger) Status() TriggerStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TriggerStatus{
		Running: t.running, LastStart: t.lastStart,
		LastEnd: t.lastEnd, LastOK: t.lastOK, LastInfo: t.lastInfo,
	}
}

func (t *CommandTrigger) run(reason string, post func(msg string)) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", t.command)
	var out cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	dur := time.Since(start).Round(time.Second)

	t.mu.Lock()
	t.running = false
	t.lastEnd = time.Now()
	t.lastOK = err == nil
	t.lastInfo = reason
	t.mu.Unlock()

	if post == nil {
		return
	}
	if err == nil {
		post(fmt.Sprintf("[Switch - eshop] sync finished in %s (%s)", dur, reason))
		return
	}
	msg := fmt.Sprintf("[Switch - eshop] sync failed after %s (%s): %s", dur, reason, err)
	if tail := out.tail(1500); tail != "" {
		msg += "\n```\n" + tail + "\n```"
	}
	post(msg)
}

// cappedBuffer keeps the tail of a command's output; a full sync run can
// log megabytes, but the staff notice only needs the ending.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

const cappedMax = 256 << 10

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) >= cappedMax {
		p = p[len(p)-cappedMax:]
	}
	if overflow := c.buf.Len() + len(p) - cappedMax; overflow > 0 {
		c.buf.Next(overflow)
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) tail(n int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.buf.String()
	for len(s) > n {
		if i := indexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		} else {
			s = s[len(s)-n:]
			break
		}
	}
	return s
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
