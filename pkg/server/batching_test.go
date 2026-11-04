package server

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/client_golang/prometheus"
)

// recordingFlush records the batches it is given, and the error of the
// context each was flushed with, and signals each one on flushed
type recordingFlush struct {
	mu      sync.Mutex
	batches [][]instanceAlarm
	ctxErrs []error
	flushed chan struct{}
}

func newRecordingFlush() *recordingFlush {
	return &recordingFlush{flushed: make(chan struct{}, 100)}
}

func (r *recordingFlush) flush(ctx context.Context, batch []instanceAlarm) {
	r.mu.Lock()
	r.batches = append(r.batches, batch)
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
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

// cancelled returns how many batches were flushed with a cancelled context
func (r *recordingFlush) cancelled() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for _, err := range r.ctxErrs {
		if err != nil {
			n++
		}
	}

	return n
}

// wait waits for a batch to be flushed, failing the test after timeout. In a
// synctest bubble the timeout is in fake time, so it only passes once nothing
// else in the bubble can run.
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
	synctest.Test(t, func(t *testing.T) {
		r := newRecordingFlush()
		b := must(newAlarmBatcher(r.flush, 10, 5, time.Hour, slog.New(slog.DiscardHandler), prometheus.NewRegistry()))(t)
		runBatcher(t, b)

		b.add(update(3))
		b.add(update(4))
		r.wait(t, time.Second)

		// a whole alarm update is added before the threshold is checked
		if got := r.sizes(); len(got) != 1 || got[0] != 7 {
			t.Errorf("flushed %v, want one batch of 7", got)
		}
	})
}

func TestAlarmBatcherFlushThresholdRestartsInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRecordingFlush()
		b := must(newAlarmBatcher(r.flush, 10, 2, time.Millisecond*200, slog.New(slog.DiscardHandler), prometheus.NewRegistry()))(t)
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
	})
}

func TestAlarmBatcherFlushInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRecordingFlush()
		b := must(newAlarmBatcher(r.flush, 10, 100, time.Millisecond*20, slog.New(slog.DiscardHandler), prometheus.NewRegistry()))(t)
		runBatcher(t, b)

		b.add(update(3))
		r.wait(t, time.Second)

		if got := r.sizes(); len(got) != 1 || got[0] != 3 {
			t.Errorf("flushed %v, want one batch of 3", got)
		}
	})
}

func TestAlarmBatcherFlushIntervalEmpty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRecordingFlush()
		b := must(newAlarmBatcher(r.flush, 10, 100, time.Millisecond*10, slog.New(slog.DiscardHandler), prometheus.NewRegistry()))(t)
		runBatcher(t, b)

		// several intervals pass with nothing to flush
		time.Sleep(time.Millisecond * 50)

		if got := r.sizes(); len(got) != 0 {
			t.Errorf("flushed %v, want nothing", got)
		}
	})
}

func TestAlarmBatcherQueueFull(t *testing.T) {
	r := newRecordingFlush()
	reg := prometheus.NewRegistry()
	b := must(newAlarmBatcher(r.flush, 2, 100, time.Hour, slog.New(slog.DiscardHandler), reg))(t)

	// nothing takes from the queue while the batcher is not running
	b.add(update(1))
	b.add(update(1))
	if got := metricValue(t, reg, "onmsgrpc_alarm_queue_depth"); got != 2 {
		t.Errorf("queueDepth = %v, want 2", got)
	}

	b.add(update(3))
	if got := metricValue(t, reg, "onmsgrpc_alarm_dropped_total"); got != 3 {
		t.Errorf("dropped = %v, want 3 for the alarms in the update that did not fit", got)
	}
	if got := metricValue(t, reg, "onmsgrpc_alarm_queue_depth"); got != 2 {
		t.Errorf("queueDepth = %v, want 2 after a drop", got)
	}
}

func TestAlarmBatcherQueueDepthAfterFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRecordingFlush()
		reg := prometheus.NewRegistry()
		b := must(newAlarmBatcher(r.flush, 10, 1, time.Hour, slog.New(slog.DiscardHandler), reg))(t)
		runBatcher(t, b)

		b.add(update(1))
		r.wait(t, time.Second)
		synctest.Wait()

		if got := metricValue(t, reg, "onmsgrpc_alarm_queue_depth"); got != 0 {
			t.Errorf("queueDepth = %v, want 0 once the queue is flushed", got)
		}
	})
}

func TestAlarmBatcherShutdownFlushesBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRecordingFlush()
		b := must(newAlarmBatcher(r.flush, 10, 100, time.Hour, slog.New(slog.DiscardHandler), prometheus.NewRegistry()))(t)
		cancel, done := runBatcher(t, b)

		b.add(update(3))

		// wait for the update to be taken from the queue into the batch
		synctest.Wait()
		if got := len(b.queue); got != 0 {
			t.Fatalf("queue holds %d updates, want the update taken into the batch", got)
		}

		cancel()
		<-done

		if got := r.sizes(); len(got) != 1 || got[0] != 3 {
			t.Errorf("flushed %v, want the batch of 3 flushed on shutdown", got)
		}
		if got := r.cancelled(); got != 0 {
			t.Errorf("%d batches flushed with a cancelled context, want none so the alerts are still sent", got)
		}
	})
}

func TestAlarmBatcherShutdownFlushesQueue(t *testing.T) {
	r := newRecordingFlush()

	// hold the first flush so later updates wait in the queue
	gate := make(chan struct{})
	var once sync.Once
	flush := func(ctx context.Context, batch []instanceAlarm) {
		once.Do(func() { <-gate })
		r.flush(ctx, batch)
	}

	reg := prometheus.NewRegistry()
	b := must(newAlarmBatcher(flush, 10, 1, time.Hour, slog.New(slog.DiscardHandler), reg))(t)
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

	// the first flush was in progress when the batcher was stopped
	if got := r.cancelled(); got != 0 {
		t.Errorf("%d batches flushed with a cancelled context, want none so the alerts are still sent", got)
	}
	if got := metricValue(t, reg, "onmsgrpc_alarm_queue_depth"); got != 0 {
		t.Errorf("queueDepth = %v, want 0 after shutdown", got)
	}
}

// TestAlarmBatcherShutdownFlushTimeout checks flushes that are still running
// once the batcher is stopped, whether the final flush or one already in
// progress, are cancelled after shutdownFlushTimeout
func TestAlarmBatcherShutdownFlushTimeout(t *testing.T) {
	tests := []struct {
		name           string
		flushThreshold int
	}{
		{"final flush", 100},
		{"flush in progress", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// each flush waits for its context to be cancelled, as for
				// an Alertmanager that does not respond
				var mu sync.Mutex
				var stoppedAt time.Time
				var waited []time.Duration
				flush := func(ctx context.Context, batch []instanceAlarm) {
					<-ctx.Done()

					mu.Lock()
					waited = append(waited, time.Since(stoppedAt))
					mu.Unlock()
				}

				b := must(newAlarmBatcher(flush, 10, tt.flushThreshold, time.Hour, slog.New(slog.DiscardHandler), prometheus.NewRegistry()))(t)
				cancel, done := runBatcher(t, b)

				b.add(update(1))
				synctest.Wait()

				mu.Lock()
				stoppedAt = time.Now()
				mu.Unlock()
				cancel()
				<-done

				if len(waited) != 1 || waited[0] != shutdownFlushTimeout {
					t.Errorf("flushes cancelled after %v, want one cancelled after %v", waited, shutdownFlushTimeout)
				}
			})
		})
	}
}

func TestAlarmBatcherAddAfterShutdown(t *testing.T) {
	r := newRecordingFlush()
	reg := prometheus.NewRegistry()
	b := must(newAlarmBatcher(r.flush, 10, 100, time.Hour, slog.New(slog.DiscardHandler), reg))(t)
	cancel, done := runBatcher(t, b)

	cancel()
	<-done

	b.add(update(2))
	if got := metricValue(t, reg, "onmsgrpc_alarm_dropped_total"); got != 2 {
		t.Errorf("dropped = %v, want 2 for the update added after shutdown", got)
	}
	if got := len(b.queue); got != 0 {
		t.Errorf("queue holds %d updates, want none after shutdown", got)
	}
}
