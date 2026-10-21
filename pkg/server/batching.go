package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// alarmBatcher queues alarm updates and flushes their alarms in batches,
// either once the batch reaches the flush threshold or at each flush
// interval. A single worker flushes, so a slow flush holds up the queue.
type alarmBatcher struct {
	flush          func([]instanceAlarm)
	flushThreshold int
	flushInterval  time.Duration
	logger         *slog.Logger

	// queue of alarm updates waiting to be batched
	queue chan []instanceAlarm

	// metrics
	queueDepth prometheus.Gauge
	dropped    prometheus.Counter
}

// newAlarmBatcher returns an alarmBatcher that holds up to queueSize alarm
// updates and passes each batch to flush
func newAlarmBatcher(flush func([]instanceAlarm), queueSize, flushThreshold int, flushInterval time.Duration, logger *slog.Logger) *alarmBatcher {
	return &alarmBatcher{
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
			Help: "Total number of alarms dropped due to the queue being full.",
		}),
	}
}

// collectors returns the metrics of the alarmBatcher for registration
func (b *alarmBatcher) collectors() []prometheus.Collector {
	return []prometheus.Collector{b.queueDepth, b.dropped}
}

// add queues the alarms of an alarm update, dropping them if the queue is
// full
func (b *alarmBatcher) add(alarms []instanceAlarm) {
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

// run batches and flushes queued alarms until ctx is cancelled, when the
// current batch is flushed
func (b *alarmBatcher) run(ctx context.Context) {
	var batch []instanceAlarm
	timer := time.NewTimer(b.flushInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			if len(batch) > 0 {
				b.logger.Info("batchWorker: flushing on shutdown", "alarmcount", len(batch))
				b.flush(batch)
				b.queueDepth.Set(0)
			}
			return

		case alarms := <-b.queue:
			batch = append(batch, alarms...)

			// the threshold is checked once a whole alarm update is added,
			// so a batch may be larger than the threshold
			if len(batch) >= b.flushThreshold {
				b.logger.Info("batchWorker: flushing on size", "alarmcount", len(batch))
				b.flush(batch)
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
				b.flush(batch)
				batch = nil
				b.queueDepth.Set(float64(len(b.queue)))
			}
			timer.Reset(b.flushInterval)
		}
	}
}
