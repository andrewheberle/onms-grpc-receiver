package server

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// recordingFlush records the batches it is given and signals each one on
// flushed
type recordingFlush struct {
	mu      sync.Mutex
	batches [][]instanceAlarm
	flushed chan struct{}
}

func newRecordingFlush() *recordingFlush {
	return &recordingFlush{flushed: make(chan struct{}, 100)}
}

func (r *recordingFlush) flush(batch []instanceAlarm) {
	r.mu.Lock()
	r.batches = append(r.batches, batch)
	r.mu.Unlock()

	r.flushed <- struct{}{}
}

// sizes returns the number of alarms in each batch flushed so far
func (r *recordingFlush) sizes() []int {
	r.mu.Lock()
	defer r.mu.Unlock()

	sizes := make([]int, 0, len(r.batches))
	for _, b := range r.batches {
		sizes = append(sizes, len(b))
	}

	return sizes
}

// wait waits for a batch to be flushed, failing the test after timeout
func (r *recordingFlush) wait(t *testing.T, timeout time.Duration) {
	t.Helper()

	select {
	case <-r.flushed:
	case <-time.After(timeout):
		t.Fatalf("no batch flushed within %v, flushed %v", timeout, r.sizes())
	}
}

// update returns the alarms of an alarm update with n alarms
func update(n int) []instanceAlarm {
	alarms := make([]instanceAlarm, 0, n)
	for i := range n {
		alarms = append(alarms, instanceAlarm{
			alarm:      pb.Alarm_builder{Id: uint64(i)}.Build(),
			instanceID: "test",
			now:        time.Now(),
		})
	}

	return alarms
}

// runBatcher runs b until the test ends, returning a channel that is closed
// once run returns
func runBatcher(t *testing.T, b *alarmBatcher) (context.CancelFunc, <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	return cancel, done
}

func TestAlarmBatcherFlushThreshold(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 10, 5, time.Hour, slog.New(slog.DiscardHandler))
	runBatcher(t, b)

	b.add(update(3))
	b.add(update(4))
	r.wait(t, time.Second)

	// a whole alarm update is added before the threshold is checked
	if got := r.sizes(); len(got) != 1 || got[0] != 7 {
		t.Errorf("flushed %v, want one batch of 7", got)
	}
}

func TestAlarmBatcherFlushThresholdRestartsInterval(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 10, 2, time.Millisecond*200, slog.New(slog.DiscardHandler))
	runBatcher(t, b)

	// flush on the threshold just before the interval would have fired
	time.Sleep(time.Millisecond * 150)
	b.add(update(2))
	r.wait(t, time.Second)

	// a batch added now is only flushed a whole interval after the
	// threshold flush, not when the original interval ended
	b.add(update(1))
	time.Sleep(time.Millisecond * 100)
	if got := r.sizes(); len(got) != 1 {
		t.Fatalf("flushed %v, want only the threshold flush before the interval restarts", got)
	}

	r.wait(t, time.Second)
	if got := r.sizes(); len(got) != 2 || got[1] != 1 {
		t.Errorf("flushed %v, want a second batch of 1", got)
	}
}

func TestAlarmBatcherFlushInterval(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 10, 100, time.Millisecond*20, slog.New(slog.DiscardHandler))
	runBatcher(t, b)

	b.add(update(3))
	r.wait(t, time.Second)

	if got := r.sizes(); len(got) != 1 || got[0] != 3 {
		t.Errorf("flushed %v, want one batch of 3", got)
	}
}

func TestAlarmBatcherFlushIntervalEmpty(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 10, 100, time.Millisecond*10, slog.New(slog.DiscardHandler))
	runBatcher(t, b)

	// several intervals pass with nothing to flush
	time.Sleep(time.Millisecond * 50)

	if got := r.sizes(); len(got) != 0 {
		t.Errorf("flushed %v, want nothing", got)
	}
}

func TestAlarmBatcherQueueFull(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 2, 100, time.Hour, slog.New(slog.DiscardHandler))

	// nothing takes from the queue while the batcher is not running
	b.add(update(1))
	b.add(update(1))
	if got := testutil.ToFloat64(b.queueDepth); got != 2 {
		t.Errorf("queueDepth = %v, want 2", got)
	}

	b.add(update(3))
	if got := testutil.ToFloat64(b.dropped); got != 3 {
		t.Errorf("dropped = %v, want 3 for the alarms in the update that did not fit", got)
	}
	if got := testutil.ToFloat64(b.queueDepth); got != 2 {
		t.Errorf("queueDepth = %v, want 2 after a drop", got)
	}
}

func TestAlarmBatcherQueueDepthAfterFlush(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 10, 1, time.Hour, slog.New(slog.DiscardHandler))
	runBatcher(t, b)

	b.add(update(1))
	r.wait(t, time.Second)

	if got := testutil.ToFloat64(b.queueDepth); got != 0 {
		t.Errorf("queueDepth = %v, want 0 once the queue is flushed", got)
	}
}

func TestAlarmBatcherShutdownFlushesBatch(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 10, 100, time.Hour, slog.New(slog.DiscardHandler))
	cancel, done := runBatcher(t, b)

	b.add(update(3))

	// wait for the update to be taken from the queue into the batch
	deadline := time.Now().Add(time.Second)
	for len(b.queue) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("update was not taken from the queue")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	<-done

	if got := r.sizes(); len(got) != 1 || got[0] != 3 {
		t.Errorf("flushed %v, want the batch of 3 flushed on shutdown", got)
	}
}

func TestAlarmBatcherShutdownFlushesQueue(t *testing.T) {
	r := newRecordingFlush()

	// hold the first flush so later updates wait in the queue
	gate := make(chan struct{})
	var once sync.Once
	flush := func(batch []instanceAlarm) {
		once.Do(func() { <-gate })
		r.flush(batch)
	}

	b := newAlarmBatcher(flush, 10, 1, time.Hour, slog.New(slog.DiscardHandler))
	cancel, done := runBatcher(t, b)

	b.add(update(1))
	b.add(update(2))
	b.add(update(3))

	cancel()
	close(gate)
	<-done

	total := 0
	for _, n := range r.sizes() {
		total += n
	}
	if total != 6 {
		t.Errorf("flushed %v, want all 6 alarms flushed on shutdown", r.sizes())
	}
	if got := testutil.ToFloat64(b.queueDepth); got != 0 {
		t.Errorf("queueDepth = %v, want 0 after shutdown", got)
	}
}

func TestAlarmBatcherAddAfterShutdown(t *testing.T) {
	r := newRecordingFlush()
	b := newAlarmBatcher(r.flush, 10, 100, time.Hour, slog.New(slog.DiscardHandler))
	cancel, done := runBatcher(t, b)

	cancel()
	<-done

	b.add(update(2))
	if got := testutil.ToFloat64(b.dropped); got != 2 {
		t.Errorf("dropped = %v, want 2 for the update added after shutdown", got)
	}
	if got := len(b.queue); got != 0 {
		t.Errorf("queue holds %d updates, want none after shutdown", got)
	}
}
