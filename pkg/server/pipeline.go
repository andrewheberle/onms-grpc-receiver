package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/client_golang/prometheus"
)

// alarmQueueSize is the number of alarm updates that may wait to be batched
const alarmQueueSize = 100

// instanceAlarm is an alarm from a Horizon instance waiting to be flushed, or
// with no alarm, the end of a snapshot from the instance
type instanceAlarm struct {
	alarm        *pb.Alarm
	now          time.Time
	instanceID   string
	instanceName string

	// endOfSnapshot is set, with no alarm, after the alarms of a snapshot
	endOfSnapshot bool
}

// alarmCount returns the number of alarms in alarms, not counting the ends of
// snapshots
func alarmCount(alarms []instanceAlarm) int {
	n := 0
	for _, ia := range alarms {
		if ia.alarm != nil {
			n++
		}
	}

	return n
}

// alertSender sends alerts to every Alertmanager currently known
type alertSender interface {
	// send sends alerts, returning an error describing every Alertmanager
	// that did not accept them, or that the Alertmanagers could not be
	// found. ctx bounds the whole send.
	send(ctx context.Context, alerts []*postableAlert) error
}

// alertPipeline handles alarm updates and heartbeats once they are received,
// through alert translation to alert delivery. Alarms are batched before they
// are sent, while heartbeats are sent as they are received.
type alertPipeline struct {
	translator *alertTranslator
	sender     alertSender
	batcher    *alarmBatcher
	logger     *slog.Logger

	// the Firing severity of each alarm, only used while flushing
	memory *firingMemory

	// log every alarm in full
	logAlarms bool

	// metrics
	alarmTotal        *prometheus.CounterVec
	alarmCount        *prometheus.GaugeVec
	alarmLastSnapshot *prometheus.GaugeVec
	heartbeatTotal    *prometheus.CounterVec
}

// newAlertPipeline returns an alertPipeline that translates alarms and
// heartbeats with translator and sends them with sender, and registers its
// metrics with reg. Alarms are flushed once a batch reaches flushThreshold or
// at each flushInterval. An error is returned if the flush settings are
// invalid or metrics could not be registered.
func newAlertPipeline(translator *alertTranslator, sender alertSender, flushThreshold int, flushInterval time.Duration, logAlarms bool, logger *slog.Logger, reg prometheus.Registerer) (*alertPipeline, error) {
	p := &alertPipeline{
		translator: translator,
		sender:     sender,
		logger:     logger,
		logAlarms:  logAlarms,
		alarmTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "onmsgrpc_alarm_total",
			Help: "Total number of alarm updates seen from a Horizon instance.",
		},
			[]string{"instance_id"}),
		alarmCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "onmsgrpc_alarm_count",
			Help: "Current number of active alarms for a Horizon instance from the last full snapshot of alarms.",
		},
			[]string{"instance_id"}),
		alarmLastSnapshot: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "onmsgrpc_alarm_last_snapshot_timestamp_seconds",
			Help: "Unix time of the last full alarm snapshot received from a Horizon instance.",
		},
			[]string{"instance_id"}),
		heartbeatTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "onmsgrpc_heartbeat_total",
			Help: "Total number of heartbeat updates seen from a Horizon instance.",
		},
			[]string{"instance_id"}),
	}
	batcher, err := newAlarmBatcher(p.flush, alarmQueueSize, flushThreshold, flushInterval, logger, reg)

	// alarms are remembered for as long as a cleared alarm is still sent. The
	// translator is nil when its configuration was invalid, in which case the
	// pipeline is not used.
	var window time.Duration
	if translator != nil {
		window = translator.clearedAlarmWindow()
	}
	memory, memoryErr := newFiringMemory(window, reg)
	if err := errors.Join(err, memoryErr, register(reg, p.alarmTotal, p.alarmCount, p.alarmLastSnapshot, p.heartbeatTotal)); err != nil {
		return nil, err
	}
	p.batcher = batcher
	p.memory = memory

	return p, nil
}

// run flushes batched alarms until ctx is cancelled, when it stops accepting
// alarm updates and flushes everything already queued
func (p *alertPipeline) run(ctx context.Context) {
	p.batcher.run(ctx)
}

// alarmUpdate queues the alarms of an alarm update received at now, dropping
// them if the queue is full or the pipeline has stopped
func (p *alertPipeline) alarmUpdate(update *pb.AlarmUpdateList, now time.Time) {
	id := update.GetInstanceId()
	name := update.GetInstanceName()
	alarms := update.GetAlarms()
	isSnapshot := update.GetSnapshot()

	p.alarmTotal.WithLabelValues(id).Inc()

	if isSnapshot {
		p.alarmCount.WithLabelValues(id).Set(float64(len(alarms)))
		p.alarmLastSnapshot.WithLabelValues(id).Set(float64(now.UnixNano()) / 1e9)
	}

	p.logger.Info("AlarmUpdate",
		slog.Group("instance",
			"id", id,
			"name", name,
		),
		"snapshot", isSnapshot,
		"alarmcount", len(alarms),
	)

	// wrap alarms with instance info before enqueuing
	wrapped := make([]instanceAlarm, 0, len(alarms)+1)
	for _, alarm := range alarms {
		wrapped = append(wrapped, instanceAlarm{
			alarm:        alarm,
			instanceID:   id,
			instanceName: name,
			now:          now,
		})
	}

	// mark the end of a snapshot, even an empty one, so the firing memory
	// knows when it holds every alarm of the instance
	if isSnapshot {
		wrapped = append(wrapped, instanceAlarm{
			instanceID:    id,
			instanceName:  name,
			now:           now,
			endOfSnapshot: true,
		})
	}

	// enqueue - drop if full (best effort)
	p.batcher.add(wrapped)
}

// heartbeat sends the alert for a heartbeat received at now, which stops if
// ctx is cancelled.
//
// The alert is sent synchronously, so a slow Alertmanager holds up the
// heartbeat stream of that Horizon instance. This is deliberate: Horizon
// sends a heartbeat every 60 seconds on an asynchronous stream, and a send,
// including its retries, gives up after 10 seconds, so a backlog cannot build
// up and the only effect is a heartbeat alert arriving a few seconds late.
func (p *alertPipeline) heartbeat(ctx context.Context, hb *pb.HeartBeat, now time.Time) {
	id := hb.GetMonitoringInstance().GetInstanceId()
	name := hb.GetMonitoringInstance().GetInstanceName()

	// increment heartbeat counter
	p.heartbeatTotal.WithLabelValues(id).Inc()

	// print message
	p.logger.Info(hb.GetMessage(),
		slog.Group("instance",
			"id", id,
			"name", name,
		),
		"timestamp", hb.GetTimestamp(),
	)

	alert := p.translator.heartbeat(name, now)
	p.logger.Debug("adding message to list", "message", alert)

	// send to alertmanager at the end
	if err := p.sender.send(ctx, []*postableAlert{alert}); err != nil {
		p.logger.Error("error during send", "error", err)
	}
}

// flush translates a batch of alarms and sends the resulting alerts, which
// stops if ctx is cancelled. The Firing severity of each alarm is looked up
// before it is translated and remembered afterwards, in the order the alarms
// were received. If the alerts are not delivered, the Firing severity of every
// alarm in the batch is marked as unknown, so a Severity fan-out resolves
// whichever alert is firing. See ADR 0006.
func (p *alertPipeline) flush(ctx context.Context, alarms []instanceAlarm) {
	list := make([]*postableAlert, 0)
	keys := make([]firingKey, 0, len(alarms))
	for _, ia := range alarms {
		if ia.endOfSnapshot {
			p.memory.snapshotHandled(ia.instanceID)
			continue
		}

		if p.logAlarms {
			p.logAlarm(ia)
		}

		key := firingKey{instanceID: ia.instanceID, alarmID: ia.alarm.GetId()}
		alerts, next, remember := p.translator.alarm(ia, p.memory.lookup(key))
		if remember {
			p.memory.record(key, next, ia.now)
			keys = append(keys, key)
		} else {
			p.memory.forget(key)
		}

		list = append(list, alerts...)
	}

	// send to alertmanager at the end
	if err := p.sender.send(ctx, list); err != nil {
		p.logger.Error("error during send", "error", err)
		p.memory.markUnknown(keys)
	}

	p.memory.prune(time.Now())
}

// logAlarm logs every field of an alarm, for when alarms are logged in full
func (p *alertPipeline) logAlarm(ia instanceAlarm) {
	alarm := ia.alarm
	p.logger.Info("AlarmUpdate",
		"alarm_id", alarm.GetId(),
		"uei", alarm.GetUei(),
		slog.Group("instance",
			"id", ia.instanceID,
			"name", ia.instanceName,
		),
		slog.Group("node_criteria",
			"id", alarm.GetNodeCriteria().GetId(),
			"foreign_source", alarm.GetNodeCriteria().GetForeignSource(),
			"foreign_id", alarm.GetNodeCriteria().GetForeignId(),
			"node_label", alarm.GetNodeCriteria().GetNodeLabel(),
			"location", alarm.GetNodeCriteria().GetLocation(),
		),
		"ip_address", alarm.GetIpAddress(),
		"service_name", alarm.GetServiceName(),
		"reduction_key", alarm.GetReductionKey(),
		"type", alarm.GetType(),
		"count", alarm.GetCount(),
		"severity", alarm.GetSeverity(),
		"first_event_time", alarm.GetFirstEventTime(),
		"description", alarm.GetDescription(),
		"log_message", alarm.GetLogMessage(),
		"ack_user", alarm.GetAckUser(),
		"ack_time", alarm.GetAckTime(),
		"last_event_time", alarm.GetLastEventTime(),
		"if_index", alarm.GetIfIndex(),
		"operator_instructions", alarm.GetOperatorInstructions(),
		"clear_key", alarm.GetClearKey(),
		"managed_object_instance", alarm.GetManagedObjectInstance(),
		"managed_object_type", alarm.GetManagedObjectType(),
		"relatedAlarm_count", len(alarm.GetRelatedAlarm()),
		"last_update_time", alarm.GetLastUpdateTime(),
	)
}
