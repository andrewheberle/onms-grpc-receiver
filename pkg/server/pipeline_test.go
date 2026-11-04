package server

import (
	"context"
	"log/slog"
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
}

func (f *fakeSender) send(ctx context.Context, alerts []*postableAlert) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.alerts = append(f.alerts, alerts...)
	f.ctxErrs = append(f.ctxErrs, ctx.Err())

	return nil
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

	return newAlertPipeline(newTestTranslator(t, cfg), sender, 1000, time.Hour, false, slog.New(slog.DiscardHandler), reg), reg
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
