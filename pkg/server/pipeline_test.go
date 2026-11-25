package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/client_golang/prometheus"
)

// fakeSender is an in-memory alertSender that records the alerts it is sent,
// and the error of the context of each send
type fakeSender struct {
	mu      sync.Mutex
	alerts  []*postableAlert
	ctxErrs []error

	// err is returned from every send
	err error
}

func (f *fakeSender) send(ctx context.Context, alerts []*postableAlert) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.alerts = append(f.alerts, alerts...)
	f.ctxErrs = append(f.ctxErrs, ctx.Err())

	return f.err
}

// sent returns the alerts sent so far
func (f *fakeSender) sent() []*postableAlert {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.alerts
}

// newTestPipeline returns a pipeline that sends alerts with sender, with a
// large flush threshold and interval so alarms are only flushed when the
// pipeline stops, and the registry its metrics are registered with
func newTestPipeline(t *testing.T, sender alertSender, cfg translationConfig) (*alertPipeline, *prometheus.Registry) {
	t.Helper()

	reg := prometheus.NewRegistry()

	return must(newAlertPipeline(newTestTranslator(t, cfg), sender, 1000, time.Hour, false, slog.New(slog.DiscardHandler), reg))(t), reg
}

// runPipeline runs p while feed is called, then stops it so that every queued
// alarm is flushed
func runPipeline(p *alertPipeline, feed func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx)
	}()

	feed()

	cancel()
	<-done
}

// TestAlertPipelineAlarmUpdate checks the alarms of an alarm update are
// translated and sent to Alertmanager, the details of each alert are covered
// by the alertTranslator tests
func TestAlertPipelineAlarmUpdate(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)

	sender := &fakeSender{}
	p, _ := newTestPipeline(t, sender, translationConfig{
		extraLabels:    map[string]string{"env": "prod"},
		resolveTimeout: time.Minute * 5,
	})

	runPipeline(p, func() {
		p.alarmUpdate(pb.AlarmUpdateList_builder{
			InstanceId:   "6f8a7c1e-uuid",
			InstanceName: "OpenNMS",
			Alarms: []*pb.Alarm{
				fullAlarm(now),
				pb.Alarm_builder{
					Id:             2,
					Severity:       uint32(pb.Severity_NORMAL),
					FirstEventTime: uint64(now.UnixMilli()),
					LastEventTime:  uint64(now.UnixMilli()),
				}.Build(),
			},
		}.Build(), now)
	})

	got := sender.sent()
	if len(got) != 1 {
		t.Fatalf("sent %d alerts, want 1", len(got))
	}
	if got[0].Labels["alarm_id"] != "25" || got[0].Labels["env"] != "prod" || got[0].Labels["instance_name"] != "OpenNMS" {
		t.Errorf("labels = %v, want alarm_id 25, env prod and instance_name OpenNMS", got[0].Labels)
	}
	if want := now.Add(time.Minute * 5); !got[0].EndsAt.Equal(want) {
		t.Errorf("EndsAt = %v, want %v", got[0].EndsAt, want)
	}
}

// TestAlertPipelineLastSnapshot checks the last snapshot time is only
// recorded for full snapshots, per instance
func TestAlertPipelineLastSnapshot(t *testing.T) {
	now := time.Unix(1792540800, 500000000)

	update := func(id string, snapshot bool, alarms int) *pb.AlarmUpdateList {
		list := make([]*pb.Alarm, alarms)
		for i := range list {
			list[i] = pb.Alarm_builder{Id: uint64(i + 1)}.Build()
		}
		return pb.AlarmUpdateList_builder{
			InstanceId: id,
			Snapshot:   snapshot,
			Alarms:     list,
		}.Build()
	}

	tests := []struct {
		name    string
		updates []*pb.AlarmUpdateList
		want    []string
	}{
		{"snapshot", []*pb.AlarmUpdateList{update("a", true, 2)}, []string{"a"}},
		{"not a snapshot", []*pb.AlarmUpdateList{update("a", false, 2)}, nil},
		{"empty snapshot", []*pb.AlarmUpdateList{update("a", true, 0)}, []string{"a"}},
		{"two instances", []*pb.AlarmUpdateList{
			update("a", true, 1),
			update("b", false, 1),
			update("c", true, 1),
		}, []string{"a", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reg := newTestPipeline(t, &fakeSender{}, translationConfig{})

			runPipeline(p, func() {
				for _, u := range tt.updates {
					p.alarmUpdate(u, now)
				}
			})

			if got := metricSeries(t, reg, "onmsgrpc_alarm_last_snapshot_timestamp_seconds"); got != len(tt.want) {
				t.Fatalf("last snapshot series = %d, want %d", got, len(tt.want))
			}
			for _, id := range tt.want {
				if got := metricValue(t, reg, "onmsgrpc_alarm_last_snapshot_timestamp_seconds", "instance_id", id); got != 1792540800.5 {
					t.Errorf("last snapshot for %q = %v, want 1792540800.5", id, got)
				}
			}
		})
	}
}

// TestAlertPipelineHeartbeat checks a heartbeat is translated and sent to
// Alertmanager as it is received, the details of the alert are covered by the
// alertTranslator tests
func TestAlertPipelineHeartbeat(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)

	sender := &fakeSender{}
	p, _ := newTestPipeline(t, sender, translationConfig{
		extraLabels: map[string]string{"env": "prod"},
	})

	p.heartbeat(context.Background(), pb.HeartBeat_builder{
		MonitoringInstance: pb.MonitoringInstance_builder{
			InstanceId:   "6f8a7c1e-uuid",
			InstanceName: "OpenNMS",
		}.Build(),
		Message: "heartbeat",
	}.Build(), now)

	got := sender.sent()
	if len(got) != 1 {
		t.Fatalf("sent %d alerts, want 1", len(got))
	}
	if got[0].Labels["alertname"] != "OpenNMSHeartbeat" || got[0].Labels["env"] != "prod" {
		t.Errorf("labels = %v, want alertname OpenNMSHeartbeat and env prod", got[0].Labels)
	}
	if !got[0].StartsAt.Equal(now) {
		t.Errorf("StartsAt = %v, want %v", got[0].StartsAt, now)
	}
}

// TestAlertPipelineSendContext checks which context alerts are sent with: a
// heartbeat is sent with the context it was received with, so it stops with
// its stream, while alarms flushed as the pipeline stops are still sent
func TestAlertPipelineSendContext(t *testing.T) {
	now := time.Now()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name          string
		feed          func(p *alertPipeline)
		wantCancelled bool
	}{
		{"heartbeat with cancelled context", func(p *alertPipeline) {
			p.heartbeat(cancelled, pb.HeartBeat_builder{}.Build(), now)
		}, true},
		{"alarms flushed when stopping", func(p *alertPipeline) {
			runPipeline(p, func() {
				p.alarmUpdate(pb.AlarmUpdateList_builder{
					Alarms: []*pb.Alarm{fullAlarm(now)},
				}.Build(), now)
			})
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			p, _ := newTestPipeline(t, sender, translationConfig{resolveTimeout: time.Minute * 5})

			tt.feed(p)

			if len(sender.ctxErrs) != 1 {
				t.Fatalf("sent %d times, want 1", len(sender.ctxErrs))
			}
			if got := sender.ctxErrs[0] != nil; got != tt.wantCancelled {
				t.Errorf("sent with cancelled context = %v, want %v", got, tt.wantCancelled)
			}
		})
	}
}

// TestAlertPipelineLogsAlarms checks every alarm is logged in full only when
// alarms are logged, which is when verbose or when no Alertmanager is
// configured
func TestAlertPipelineLogsAlarms(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		logAlarms bool
		want      []string
	}{
		{"logged", true, []string{
			`msg=AlarmUpdate alarm_id=25 uei=uei.opennms.org/nodes/nodeLostService`,
			`instance.id=6f8a7c1e-uuid instance.name=OpenNMS`,
			`node_criteria.id=12 node_criteria.foreign_source=customer-a node_criteria.foreign_id=1234 node_criteria.node_label=router1 node_criteria.location=Perth`,
			`ip_address=10.0.0.1 service_name=ICMP`,
			`log_message="ICMP outage identified on interface 10.0.0.1."`,
			`operator_instructions="Check the router, see https://wiki.example.com/runbooks/icmp"`,
		}},
		{"not logged", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			sender := &fakeSender{}
			p := must(newAlertPipeline(newTestTranslator(t, translationConfig{}), sender, 1000, time.Hour, tt.logAlarms, logger, prometheus.NewRegistry()))(t)

			runPipeline(p, func() {
				p.alarmUpdate(pb.AlarmUpdateList_builder{
					InstanceId:   "6f8a7c1e-uuid",
					InstanceName: "OpenNMS",
					Alarms:       []*pb.Alarm{fullAlarm(now)},
				}.Build(), now)
			})

			// the alarm is sent either way
			if len(sender.sent()) != 1 {
				t.Errorf("sent %d alerts, want 1", len(sender.sent()))
			}

			got := logs.String()
			if !tt.logAlarms && strings.Contains(got, "alarm_id=") {
				t.Errorf("logs = %q, want no alarm logged in full", got)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("logs = %q, want containing %q", got, want)
				}
			}
		})
	}
}

// TestAlertPipelineFiringSeverity checks the Firing severity of each alarm is
// remembered across flushes, so only the alert it fired with is resolved once
// the first snapshot from its instance has been handled, and that a Severity
// fan-out is used before then and after a failed delivery
func TestAlertPipelineFiringSeverity(t *testing.T) {
	now := time.Now()
	recent := uint64(now.Add(-time.Minute).UnixMilli())

	alarm := func(id uint64, severity pb.Severity) instanceAlarm {
		return instanceAlarm{
			alarm: pb.Alarm_builder{
				Id:             id,
				Uei:            "uei.opennms.org/nodes/nodeDown",
				Severity:       uint32(severity),
				FirstEventTime: recent,
				LastEventTime:  recent,
				LastUpdateTime: recent,
			}.Build(),
			now:          now,
			instanceID:   "6f8a7c1e-uuid",
			instanceName: "OpenNMS",
		}
	}
	endOfSnapshot := instanceAlarm{now: now, instanceID: "6f8a7c1e-uuid", instanceName: "OpenNMS", endOfSnapshot: true}

	type sent struct {
		alarmID  string
		severity string
		firing   bool
	}

	steps := []struct {
		name   string
		alarms []instanceAlarm
		fail   bool
		want   []sent
	}{
		{"firing before the first snapshot", []instanceAlarm{alarm(1, pb.Severity_MAJOR)}, false, []sent{
			{"1", "major", true},
			{"1", "indeterminate", false},
			{"1", "warning", false},
			{"1", "minor", false},
			{"1", "critical", false},
		}},
		{"first snapshot", []instanceAlarm{alarm(1, pb.Severity_MAJOR), endOfSnapshot}, false, []sent{
			{"1", "major", true},
		}},
		{"new alarm after the first snapshot", []instanceAlarm{alarm(2, pb.Severity_MINOR)}, false, []sent{
			{"2", "minor", true},
		}},
		{"cleared", []instanceAlarm{alarm(2, pb.Severity_CLEARED)}, false, []sent{
			{"2", "minor", false},
		}},
		{"severity change that is not delivered", []instanceAlarm{alarm(1, pb.Severity_CRITICAL)}, true, []sent{
			{"1", "critical", true},
			{"1", "major", false},
		}},
		{"cleared after a failed delivery", []instanceAlarm{alarm(1, pb.Severity_CLEARED)}, false, []sent{
			{"1", "indeterminate", false},
			{"1", "warning", false},
			{"1", "minor", false},
			{"1", "major", false},
			{"1", "critical", false},
		}},
		{"new alarm cleared in the same batch", []instanceAlarm{alarm(3, pb.Severity_MAJOR), alarm(3, pb.Severity_CLEARED)}, false, []sent{
			{"3", "major", true},
			{"3", "major", false},
		}},
	}

	sender := &fakeSender{}
	p, _ := newTestPipeline(t, sender, translationConfig{resolveTimeout: time.Minute * 5})

	for _, step := range steps {
		sender.err = nil
		if step.fail {
			sender.err = errors.New("not delivered")
		}

		before := len(sender.sent())
		p.flush(context.Background(), step.alarms)

		var got []sent
		for _, a := range sender.sent()[before:] {
			got = append(got, sent{a.Labels["alarm_id"], a.Labels["severity"], a.EndsAt.After(now)})
		}
		if !slices.Equal(got, step.want) {
			t.Errorf("%s: sent %v, want %v", step.name, got, step.want)
		}
	}
}

// TestAlertPipelineSnapshotEnd checks the end of a snapshot, even an empty
// one, is passed on to the firing memory, and an alarm update that is not a
// snapshot is not
func TestAlertPipelineSnapshotEnd(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		snapshot bool
		alarms   int
		want     firingSeverity
	}{
		{"empty snapshot", true, 0, firingSeverity{known: true}},
		{"snapshot", true, 2, firingSeverity{known: true}},
		{"not a snapshot", false, 2, firingSeverity{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			p, _ := newTestPipeline(t, sender, translationConfig{})

			alarms := make([]*pb.Alarm, tt.alarms)
			for i := range alarms {
				alarms[i] = pb.Alarm_builder{Id: uint64(i + 1), Severity: uint32(pb.Severity_MAJOR)}.Build()
			}

			runPipeline(p, func() {
				p.alarmUpdate(pb.AlarmUpdateList_builder{
					InstanceId: "6f8a7c1e-uuid",
					Snapshot:   tt.snapshot,
					Alarms:     alarms,
				}.Build(), now)
			})

			if got := p.memory.lookup(firingKey{instanceID: "6f8a7c1e-uuid", alarmID: 100}); got != tt.want {
				t.Errorf("lookup() of an alarm not in the update = %+v, want %+v", got, tt.want)
			}
			if got := len(sender.sent()); got != tt.alarms {
				t.Errorf("sent %d alerts, want %d", got, tt.alarms)
			}
		})
	}
}
