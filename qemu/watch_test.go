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

package qemu_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	local "github.com/sergelogvinov/go-proxmox-local"
	"github.com/sergelogvinov/go-proxmox-local/fakelocal"
	"github.com/sergelogvinov/go-proxmox-local/qemu"
)

// barrierVMID is the first VMID a test's barrier uses; the recorder keeps
// barrier events out of its log.
const barrierVMID = 9000

// recorder is a qemu.EventHandler logging every call as a line such as
// "start 100 4100", "stop 100" or "running map[100:4100]".
type recorder struct {
	mu       sync.Mutex
	log      []string
	barriers map[int]chan struct{}

	// startErrs is how many of the next VMStarted calls fail.
	startErrs atomic.Int32
	// inside detects a call made while another is still running.
	inside     atomic.Int32
	concurrent atomic.Bool
}

func newRecorder() *recorder {
	return &recorder{barriers: map[int]chan struct{}{}}
}

func (r *recorder) VMStarted(_ context.Context, vmid, pid int) error {
	defer r.enter()()

	if vmid >= barrierVMID {
		return nil
	}

	if r.startErrs.Add(-1) >= 0 {
		r.record(fmt.Sprintf("failed start %d %d", vmid, pid))

		return fmt.Errorf("handler failed")
	}

	r.record(fmt.Sprintf("start %d %d", vmid, pid))

	return nil
}

func (r *recorder) VMStopped(_ context.Context, vmid int) {
	defer r.enter()()

	if vmid >= barrierVMID {
		r.mu.Lock()
		ch := r.barriers[vmid]
		r.mu.Unlock()

		if ch != nil {
			close(ch)
		}

		return
	}

	r.record(fmt.Sprintf("stop %d", vmid))
}

func (r *recorder) VMsRunning(_ context.Context, running map[int]int) {
	defer r.enter()()

	r.record(fmt.Sprintf("running %v", running))
}

func (r *recorder) enter() func() {
	if r.inside.Add(1) > 1 {
		r.concurrent.Store(true)
	}

	// Widen the window a concurrent call would be seen in.
	time.Sleep(time.Millisecond)

	return func() { r.inside.Add(-1) }
}

func (r *recorder) record(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.log = append(r.log, line)
}

func (r *recorder) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.log...)
}

// watch runs Watch on f in the background until the test ends, and
// returns once its first sync has been reported, so the run dir is
// watched from then on.
type watch struct {
	t        *testing.T
	f        *fakelocal.Fake
	r        *recorder
	barriers int
}

func startWatch(t *testing.T, f *fakelocal.Fake, opts qemu.WatchOptions) *watch {
	t.Helper()

	r := runWatch(t, f.Client().Qemu(), opts)

	require.Eventually(t, func() bool { return len(r.lines()) > 0 }, 5*time.Second, time.Millisecond, "first sync")

	return &watch{t: t, f: f, r: r}
}

// runWatch runs Watch on c in the background until the test ends.
func runWatch(t *testing.T, c *qemu.Client, opts qemu.WatchOptions) *recorder {
	t.Helper()

	r := newRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- c.Watch(ctx, r, opts) }()

	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
		assert.False(t, r.concurrent.Load(), "handler called concurrently")
	})

	return r
}

// barrier returns once Watch has handled every run dir event made before
// it: it creates and removes a pid file and waits for that stop.
func (w *watch) barrier() {
	w.t.Helper()

	vmid := barrierVMID + w.barriers
	w.barriers++

	ch := make(chan struct{})

	w.r.mu.Lock()
	w.r.barriers[vmid] = ch
	w.r.mu.Unlock()

	w.f.WritePIDFile(vmid, "")
	w.f.SetStopped(vmid)

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		w.t.Fatal("barrier timed out")
	}
}

// events returns the handler's log after the first sync.
func (w *watch) events() []string {
	return w.r.lines()[1:]
}

func watchOptions() qemu.WatchOptions {
	return qemu.WatchOptions{
		SyncInterval: time.Hour,
		MaxRetries:   3,
		BaseDelay:    10 * time.Millisecond,
		MaxDelay:     40 * time.Millisecond,
	}
}

func TestWatchCreateThenWriteIsOneStart(t *testing.T) {
	f := fakelocal.New(t)
	w := startWatch(t, f, watchOptions())

	f.SetRunning(100, 4100) // creates the pid file, then writes it
	w.barrier()

	assert.Equal(t, []string{"start 100 4100"}, w.events())
}

// TestWatchEmptyPIDFileStartsOnceFilled checks that an empty pid file is
// "not started yet", and becomes a start once it holds the PID — even
// when that PID's process only shows up after the pid file is written.
func TestWatchEmptyPIDFileStartsOnceFilled(t *testing.T) {
	f := fakelocal.New(t)

	opts := watchOptions()
	opts.MaxRetries = 1000 // keeps looking for the process while the test runs

	w := startWatch(t, f, opts)

	f.WritePIDFile(100, "")
	w.barrier()
	assert.Empty(t, w.events())

	f.WritePIDFile(100, "4100\n")
	w.barrier()
	assert.Empty(t, w.events(), "no process 4100 yet")

	f.WriteProcess(4100, "/usr/bin/kvm", "-id", "100")
	require.Eventually(t, func() bool { return len(w.events()) > 0 }, 5*time.Second, time.Millisecond)
	w.barrier()

	assert.Equal(t, []string{"start 100 4100"}, w.events())
}

func TestWatchCmdlineMismatchIsNoStart(t *testing.T) {
	f := fakelocal.New(t)
	w := startWatch(t, f, watchOptions())

	f.WriteProcess(4100, "/usr/bin/kvm", "-id", "999")
	f.WritePIDFile(100, "4100\n")
	f.WriteProcess(4101, "/bin/sleep", "100")
	f.WritePIDFile(101, "4101\n")

	// Long enough for every backoff look to run out.
	time.Sleep(200 * time.Millisecond)
	w.barrier()

	assert.Empty(t, w.events())
}

func TestWatchNewPIDIsAnotherStart(t *testing.T) {
	f := fakelocal.New(t)
	w := startWatch(t, f, watchOptions())

	f.SetRunning(100, 4100)
	w.barrier()
	f.WritePIDFile(100, "4100\n") // the same process again
	w.barrier()
	f.SetRunning(100, 4200)
	w.barrier()

	assert.Equal(t, []string{"start 100 4100", "start 100 4200"}, w.events())
}

func TestWatchRemoveIsStop(t *testing.T) {
	f := fakelocal.New(t)
	w := startWatch(t, f, watchOptions())

	f.SetRunning(100, 4100)
	w.barrier()
	f.SetStopped(100)
	f.SetRunning(100, 4100) // the same PID after a stop is a new start
	w.barrier()

	assert.Equal(t, []string{"start 100 4100", "stop 100", "start 100 4100"}, w.events())
}

func TestWatchRetriesFailedStart(t *testing.T) {
	f := fakelocal.New(t)
	w := startWatch(t, f, watchOptions())

	w.r.startErrs.Store(2)
	f.SetRunning(100, 4100)

	require.Eventually(t, func() bool { return len(w.events()) == 3 }, 5*time.Second, time.Millisecond)
	w.barrier()

	assert.Equal(t, []string{"failed start 100 4100", "failed start 100 4100", "start 100 4100"}, w.events())
}

func TestWatchGivesUpAfterMaxRetries(t *testing.T) {
	f := fakelocal.New(t)
	w := startWatch(t, f, watchOptions())

	w.r.startErrs.Store(100)
	f.SetRunning(100, 4100)

	time.Sleep(200 * time.Millisecond) // every retry is due by now
	w.barrier()

	assert.Equal(t, []string{
		"failed start 100 4100",
		"failed start 100 4100",
		"failed start 100 4100",
		"failed start 100 4100",
	}, w.events(), "one try and MaxRetries retries")
}

// TestWatchDropsStaleRetry checks that a failed start's retry is dropped
// when the guest no longer runs as that PID by the time it is due.
func TestWatchDropsStaleRetry(t *testing.T) {
	for name, change := range map[string]func(f *fakelocal.Fake){
		"pid file removed":   func(f *fakelocal.Fake) { f.SetStopped(100) },
		"pid file rewritten": func(f *fakelocal.Fake) { f.WritePIDFile(100, "4200\n") },
		// No run dir event: only the retry's own check can see it.
		"process gone": func(f *fakelocal.Fake) { f.WriteProcess(4100, "/bin/sleep", "100") },
	} {
		t.Run(name, func(t *testing.T) {
			f := fakelocal.New(t)

			opts := watchOptions()
			opts.BaseDelay = 100 * time.Millisecond
			opts.MaxDelay = 100 * time.Millisecond

			w := startWatch(t, f, opts)

			w.r.startErrs.Store(1000)
			f.SetRunning(100, 4100)
			w.barrier()

			before := len(w.events())
			change(f) // before the retry is due; no process 4200 exists
			w.barrier()
			time.Sleep(300 * time.Millisecond)
			w.barrier()

			events := w.events()
			assert.Equal(t, []string{"failed start 100 4100"}, events[:before], "one try before the change")

			for _, line := range events[before:] {
				assert.NotContains(t, line, "start", "after the change")
			}
		})
	}
}

// TestWatchSyncSeedsReportedPIDs checks that a guest seen running by a sync
// is not started again by a later write of the same PID, but is by a new
// one.
func TestWatchSyncSeedsReportedPIDs(t *testing.T) {
	f := fakelocal.New(t,
		fakelocal.WithGuest(100, "name: vm100\n"),
		fakelocal.WithRunning(100, 4100),
		fakelocal.WithGuest(101, "name: tmpl\ntemplate: 1\n"),
		fakelocal.WithRunning(101, 4101),
		fakelocal.WithGuest(102, "name: stopped\n"),
	)
	w := startWatch(t, f, watchOptions())

	assert.Equal(t, []string{"running map[100:4100]"}, w.r.lines(), "templates left out")

	f.WritePIDFile(100, "4100\n")
	w.barrier()
	assert.Empty(t, w.events())

	f.SetRunning(100, 4200)
	w.barrier()
	assert.Equal(t, []string{"start 100 4200"}, w.events())
}

// TestWatchSyncKeepsPendingStart checks that a sync does not swallow a VM
// start that is still being looked for: a guest "not started yet" that a
// sync then sees running is reported by its next look.
func TestWatchSyncKeepsPendingStart(t *testing.T) {
	f := fakelocal.New(t, fakelocal.WithGuest(100, "name: vm100\n"))

	opts := watchOptions()
	opts.SyncInterval = 10 * time.Millisecond
	opts.BaseDelay = time.Hour // the pending look waits for the next file event
	opts.MaxDelay = time.Hour

	w := startWatch(t, f, opts)

	f.WritePIDFile(100, "4100\n") // no process 4100 yet: not started yet
	w.barrier()

	// The process shows up with no run dir event; a sync sees it first.
	f.WriteProcess(4100, "/usr/bin/kvm", "-id", "100")
	require.Eventually(t, func() bool {
		lines := w.r.lines()

		return lines[len(lines)-1] == "running map[100:4100]"
	}, 5*time.Second, time.Millisecond)

	f.WritePIDFile(100, "4100\n")
	w.barrier()

	assert.Contains(t, w.events(), "start 100 4100")
}

func TestWatchSyncsPeriodically(t *testing.T) {
	f := fakelocal.New(t, fakelocal.WithGuest(100, "name: vm100\n"))

	opts := watchOptions()
	opts.SyncInterval = 20 * time.Millisecond

	w := startWatch(t, f, opts)

	f.SetRunning(100, 4100)

	require.Eventually(t, func() bool {
		events := w.events()

		return len(events) > 0 && events[len(events)-1] == "running map[100:4100]"
	}, 5*time.Second, time.Millisecond)

	assert.Equal(t, "running map[]", w.r.lines()[0])
	assert.Contains(t, w.events(), "start 100 4100")
}

// TestWatchRetriesFailedSync checks that a sync that cannot list the
// guests tells the handler nothing, and is retried.
func TestWatchRetriesFailedSync(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "run", "qemu-server"), 0o755))

	client, err := local.New(local.WithRoot(root))
	require.NoError(t, err)

	opts := watchOptions()
	opts.BaseDelay = 50 * time.Millisecond
	opts.MaxDelay = 50 * time.Millisecond

	r := runWatch(t, client.Qemu(), opts)

	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, r.lines(), "no qemu-server config dir: the sync fails")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc", "pve", "qemu-server"), 0o755))

	require.Eventually(t, func() bool { return len(r.lines()) > 0 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, []string{"running map[]"}, r.lines())
}

// TestWatchCallsHandlerSerially drives starts, failed starts, stops and
// syncs at once; the recorder (and the race detector) fail the test on a
// concurrent call.
func TestWatchCallsHandlerSerially(t *testing.T) {
	var guests []fakelocal.Option
	for vmid := 100; vmid < 120; vmid++ {
		guests = append(guests, fakelocal.WithGuest(vmid, "name: vm\n"))
	}

	f := fakelocal.New(t, guests...)

	opts := watchOptions()
	opts.SyncInterval = time.Millisecond
	opts.BaseDelay = time.Millisecond
	opts.MaxDelay = time.Millisecond

	w := startWatch(t, f, opts)
	w.r.startErrs.Store(20)

	for vmid := 100; vmid < 120; vmid++ {
		f.SetRunning(vmid, vmid+4000)
	}

	for vmid := 100; vmid < 110; vmid++ {
		f.SetStopped(vmid)
	}

	time.Sleep(100 * time.Millisecond)
	w.barrier()

	assert.False(t, w.r.concurrent.Load())
}
