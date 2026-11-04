package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// alarmBatcher queues alarm updates and flushes their alarms in batches,
// either once the batch reaches the flush threshold or at each flush
// interval. A single worker flushes, so a slow flush holds up the queue.
type alarmBatcher struct {
	flush          func(context.Context, []instanceAlarm)
	flushThreshold int
	flushInterval  time.Duration
	logger         *slog.Logger

	// queue of alarm updates waiting to be batched
	queue chan []instanceAlarm

	// stopped is set once run is shutting down, after which no more alarm
	// updates are queued
	mu      sync.RWMutex
	stopped bool

	// metrics
	queueDepth prometheus.Gauge
	dropped    prometheus.Counter
}

// newAlarmBatcher returns an alarmBatcher that holds up to queueSize alarm
// updates and passes each batch to flush, and registers its metrics with reg.
// The error returned joins every problem: a flush threshold less than one, a
// flush interval that is not greater than zero, or metrics that could not be
// registered.
func newAlarmBatcher(flush func(context.Context, []instanceAlarm), queueSize, flushThreshold int, flushInterval time.Duration, logger *slog.Logger, reg prometheus.Registerer) (*alarmBatcher, error) {
	var problems []error
	if flushThreshold < 1 {
		problems = append(problems, fmt.Errorf("invalid flush threshold %d: must be at least 1", flushThreshold))
	}
	if flushInterval <= 0 {
		problems = append(problems, fmt.Errorf("invalid flush interval %s: must be greater than zero", flushInterval))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	b := &alarmBatcher{
		flush:          flush,
		flushThreshold: flushThreshold,
		flushInterval:  flushInterval,
		logger:         logger,
		queue:          make(chan []instanceAlarm, queueSize),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "onmsgrpc_alarm_queue_depth",
			Help: "Current number of alarm batches waiting in the queue.",
		}),
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "onmsgrpc_alarm_dropped_total",
			Help: "Total number of alarms dropped due to the queue being full or the server stopping.",
		}),
	}
	if err := register(reg, b.queueDepth, b.dropped); err != nil {
		return nil, err
	}

	return b, nil
}

// add queues the alarms of an alarm update, dropping them if the queue is
// full or the batcher has stopped
func (b *alarmBatcher) add(alarms []instanceAlarm) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.stopped {
		b.logger.Warn("alarm update received while stopping, dropping batch", "alarmcount", len(alarms))
		b.dropped.Add(float64(len(alarms)))
		return
	}

	select {
	case b.queue <- alarms:
		b.queueDepth.Set(float64(len(b.queue)))
	default:
		// all alarms in an update are from the same instance
		logger := b.logger
		if len(alarms) > 0 {
			logger = logger.With("instance_id", alarms[0].instanceID)
		}
		logger.Warn("alarm queue full, dropping batch", "alarmcount", len(alarms))
		b.dropped.Add(float64(len(alarms)))
	}
}

// shutdownFlushTimeout is how long flushes have to finish once the batcher
// is stopped, so that stopping fits within the 10 second grace period of
// docker stop. See ADR 0004.
const shutdownFlushTimeout = 5 * time.Second

// run batches and flushes queued alarms until ctx is cancelled, when it stops
// queueing alarm updates and flushes everything already queued.
//
// Flushes are given a context that is not cancelled with ctx, so a flush in
// progress when ctx is cancelled, and the final flush on shutdown, still send
// their alerts. Instead, it is cancelled shutdownFlushTimeout after ctx.
func (b *alarmBatcher) run(ctx context.Context) {
	flushCtx, cancelFlush := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelFlush()
	stopShutdownTimer := context.AfterFunc(ctx, func() {
		timer := time.NewTimer(shutdownFlushTimeout)
		defer timer.Stop()

		select {
		case <-timer.C:
			cancelFlush()
		case <-flushCtx.Done():
		}
	})
	defer stopShutdownTimer()

	var batch []instanceAlarm
	timer := time.NewTimer(b.flushInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// once stopped no more updates are queued, so the queue can be
			// drained into the final batch
			b.mu.Lock()
			b.stopped = true
			b.mu.Unlock()

			for len(b.queue) > 0 {
				batch = append(batch, <-b.queue...)
			}

			if len(batch) > 0 {
				b.logger.Info("batchWorker: flushing on shutdown", "alarmcount", len(batch))
				b.flush(flushCtx, batch)
			}
			b.queueDepth.Set(0)
			return

		case alarms := <-b.queue:
			batch = append(batch, alarms...)

			// the threshold is checked once a whole alarm update is added,
			// so a batch may be larger than the threshold
			if len(batch) >= b.flushThreshold {
				b.logger.Info("batchWorker: flushing on size", "alarmcount", len(batch))
				b.flush(flushCtx, batch)
				batch = nil
				b.queueDepth.Set(float64(len(b.queue)))

				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(b.flushInterval)
			}

		case <-timer.C:
			if len(batch) > 0 {
				b.logger.Info("batchWorker: flushing on timer", "alarmcount", len(batch))
				b.flush(flushCtx, batch)
				batch = nil
				b.queueDepth.Set(float64(len(b.queue)))
			}
			timer.Reset(b.flushInterval)
		}
	}
}
