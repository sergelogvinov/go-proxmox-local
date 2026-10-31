/*
Copyright 2026 Proxmox Community.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package qemu

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// EventHandler is told which guests start, stop and run on this node; see
// Watch. Its methods are called from one goroutine, never concurrently.
type EventHandler interface {
	// VMStarted is called on a VM start: a new QEMU process for guest
	// vmid, checked as that guest's (see Watch). An error has the start
	// retried.
	VMStarted(ctx context.Context, vmid, pid int) error
	// VMStopped is called when guest vmid's pid file is removed: its
	// process is gone.
	VMStopped(ctx context.Context, vmid int)
	// VMsRunning is called on every sync with each running guest (VMID to
	// PID), templates left out, and only when every guest's state is
	// known.
	VMsRunning(ctx context.Context, running map[int]int)
}

// WatchOptions configures Watch.
type WatchOptions struct {
	// SyncInterval is how often the running guests are passed to
	// EventHandler.VMsRunning, besides once when Watch starts. Zero
	// syncs only at start.
	SyncInterval time.Duration
	// MaxRetries is how many times a failed VM start, a guest not
	// started yet, or a failed sync is looked at again. Zero never
	// retries.
	MaxRetries int
	// BaseDelay is the first retry's delay, doubled on each next one
	// (default 2s).
	BaseDelay time.Duration
	// MaxDelay caps the retry delay (default 30s).
	MaxDelay time.Duration
}

const (
	// defaultBaseDelay is WatchOptions.BaseDelay when unset.
	defaultBaseDelay = 2 * time.Second
	// defaultMaxDelay is WatchOptions.MaxDelay when unset.
	defaultMaxDelay = 30 * time.Second
	// idle is how long the loop waits when nothing is due.
	idle = time.Hour
)

func (o WatchOptions) withDefaults() WatchOptions {
	if o.BaseDelay <= 0 {
		o.BaseDelay = defaultBaseDelay
	}

	if o.MaxDelay <= 0 {
		o.MaxDelay = defaultMaxDelay
	}

	return o
}

// delay is the backoff before retry number attempt+1.
func (o WatchOptions) delay(attempt int) time.Duration {
	d := o.BaseDelay
	for range attempt {
		if d >= o.MaxDelay {
			break
		}

		d *= 2
	}

	return min(d, o.MaxDelay)
}

// Watch watches the run dir and tells handler of every VM start and VM
// stop on this node, and of the running guests on every sync, until ctx
// is canceled; it then returns nil. It returns an error only when the
// run dir cannot be watched.
//
// The handler is called from Watch's goroutine, one call at a time,
// never concurrently.
//
// A create or write of <vmid>.pid only has Watch look at the guest
// again. VMStarted(vmid, pid) is called once the guest runs as pid by the
// same check List's Status uses (the pid file holds a positive PID whose
// command line has "-id <vmid>"), and only if pid is not the PID last
// reported for that guest: a new PID is a restart and reported again.
// An empty or unparsable pid file, or a command line of another process,
// is "not started yet": the guest is looked at again with backoff, up
// to MaxRetries times.
//
// A VMStarted error is retried with backoff, up to MaxRetries times. Each
// retry checks the guest again first, and is dropped if it no longer runs
// as that PID. A file event replaces a guest's pending look; it keeps a
// pending retry whose PID the guest still runs as, with its backoff.
//
// A removal of <vmid>.pid calls VMStopped(vmid) and forgets the guest's
// reported PID and pending look. A pid file left behind by a dead process
// gets no VMStopped; the next sync leaves the guest out.
//
// A sync runs at start, every SyncInterval, and after a watcher error
// (events may have been lost). It lists the running guests as List does,
// templates skipped, and passes them to VMsRunning. Their PIDs count as
// reported, so a later write of the same PID is no VM start. A failed
// or partial list (a *ListError) calls nothing and is retried with
// backoff, up to MaxRetries times.
func (c *Client) Watch(ctx context.Context, handler EventHandler, opts WatchOptions) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create a file watcher: %w", err)
	}

	defer watcher.Close() //nolint:errcheck

	if err := watcher.Add(c.env.RunDir()); err != nil {
		return fmt.Errorf("failed to watch %s: %w", c.env.RunDir(), err)
	}

	w := &watchLoop{
		client:   c,
		handler:  handler,
		opts:     opts.withDefaults(),
		reported: map[int]int{},
		pending:  map[int]*pendingStart{},
	}

	var tick <-chan time.Time

	if opts.SyncInterval > 0 {
		ticker := time.NewTicker(opts.SyncInterval)
		defer ticker.Stop()

		tick = ticker.C
	}

	w.sync(ctx)

	retry := time.NewTimer(0)
	defer retry.Stop()

	for {
		retry.Reset(w.nextDue())

		select {
		case <-ctx.Done():
			return nil

		case <-tick:
			w.sync(ctx)

		case <-retry.C:
			w.retryDue(ctx)

		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("file watcher of %s closed", c.env.RunDir())
			}

			w.fileEvent(ctx, event)

		case _, ok := <-watcher.Errors: //nolint:errcheck
			if !ok {
				return fmt.Errorf("file watcher of %s closed", c.env.RunDir())
			}

			// Events may have been lost (an inotify queue overflow, say):
			// a sync tells the handler what runs.
			w.sync(ctx)
		}
	}
}

// watchLoop is Watch's state, owned by its goroutine.
type watchLoop struct {
	client  *Client
	handler EventHandler
	opts    WatchOptions

	// reported is the PID last reported as started, or seen running by a
	// sync, for each guest.
	reported map[int]int
	// pending is each guest's next look, if one is due.
	pending map[int]*pendingStart

	// syncDue is when a failed sync is retried; zero if none is.
	syncDue time.Time
	// syncAttempts counts the retries of a failing sync.
	syncAttempts int
}

// pendingStart is a guest's next look for a VM start.
type pendingStart struct {
	// pid is the PID whose VMStarted failed and is retried; 0 while the
	// guest has not started yet.
	pid      int
	attempts int
	due      time.Time
}

// nextDue is how long until the earliest pending look.
func (w *watchLoop) nextDue() time.Duration {
	next := idle

	for _, p := range w.pending {
		next = min(next, time.Until(p.due))
	}

	if !w.syncDue.IsZero() {
		next = min(next, time.Until(w.syncDue))
	}

	return max(next, 0)
}

// retryDue runs a failed sync and every pending look that are due.
func (w *watchLoop) retryDue(ctx context.Context) {
	now := time.Now()

	if !w.syncDue.IsZero() && !w.syncDue.After(now) {
		w.syncDue = time.Time{}
		w.syncAttempts++
		w.retrySync(ctx)
	}

	for vmid, p := range w.pending {
		if p.due.After(now) {
			continue
		}

		delete(w.pending, vmid)
		w.look(ctx, vmid, p)
	}
}

// schedule has guest vmid looked at again after a backoff, unless prev
// has used up its retries.
func (w *watchLoop) schedule(vmid, pid int, prev *pendingStart) {
	attempts := 0
	if prev != nil && prev.pid == pid {
		attempts = prev.attempts
	}

	if attempts >= w.opts.MaxRetries {
		return
	}

	w.pending[vmid] = &pendingStart{
		pid:      pid,
		attempts: attempts + 1,
		due:      time.Now().Add(w.opts.delay(attempts)),
	}
}

func (w *watchLoop) fileEvent(ctx context.Context, event fsnotify.Event) {
	vmid, ok := pidFileVMID(event.Name)
	if !ok {
		return
	}

	switch {
	case event.Has(fsnotify.Remove):
		delete(w.reported, vmid)
		delete(w.pending, vmid)
		w.handler.VMStopped(ctx, vmid)

	case event.Has(fsnotify.Create), event.Has(fsnotify.Write):
		// A file event replaces the guest's pending look, unless that is a
		// retry of the start the file still names: that keeps its backoff.
		if p := w.pending[vmid]; p != nil && p.pid != 0 {
			if status, pid := w.client.status(vmid); status == VMStatusRunning && pid == p.pid {
				return
			}
		}

		delete(w.pending, vmid)
		w.look(ctx, vmid, nil)
	}
}

// look checks whether guest vmid has started since it was last reported,
// and reports the start if so. prev is the pending look this one retries,
// nil for a fresh one.
func (w *watchLoop) look(ctx context.Context, vmid int, prev *pendingStart) {
	status, pid := w.client.status(vmid)

	if prev != nil && prev.pid != 0 {
		// A retry of a failed VMStarted: dropped unless the guest still
		// runs as that PID.
		if status != VMStatusRunning || pid != prev.pid {
			return
		}
	} else {
		if status != VMStatusRunning {
			// Not started yet.
			w.schedule(vmid, 0, prev)

			return
		}

		if w.reported[vmid] == pid {
			return
		}
	}

	if err := w.handler.VMStarted(ctx, vmid, pid); err != nil {
		w.schedule(vmid, pid, prev)

		return
	}

	w.reported[vmid] = pid
}

// sync passes the running guests to the handler, and seeds the reported
// PIDs with them. A failed sync, a partial list included, tells the
// handler nothing and is retried with backoff.
func (w *watchLoop) sync(ctx context.Context) {
	w.syncDue = time.Time{}
	w.syncAttempts = 0
	w.retrySync(ctx)
}

// retrySync is sync, as retry number syncAttempts.
func (w *watchLoop) retrySync(ctx context.Context) {
	guests, err := w.client.List(ctx, ListFilter{Status: VMStatusRunning, SkipTemplates: true})
	if err != nil {
		if w.syncAttempts < w.opts.MaxRetries {
			w.syncDue = time.Now().Add(w.opts.delay(w.syncAttempts))
		}

		return
	}

	running := make(map[int]int, len(guests))
	for _, g := range guests {
		running[g.VMID] = g.PID
		w.reported[g.VMID] = g.PID
	}

	w.handler.VMsRunning(ctx, running)
}

// pidFileVMID returns the VMID a pid file is named after; false if name is
// not <vmid>.pid with a positive VMID.
func pidFileVMID(name string) (int, bool) {
	base, ok := strings.CutSuffix(filepath.Base(name), ".pid")
	if !ok {
		return 0, false
	}

	vmid, err := strconv.Atoi(base)
	if err != nil || vmid <= 0 {
		return 0, false
	}

	return vmid, true
}
