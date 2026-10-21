package server

import (
	"context"
	"log/slog"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/prometheus/client_golang/prometheus"
)

// newTestPipeline returns a pipeline that sends alerts to the Alertmanager at
// amURL, with a large flush threshold and interval so alarms are only flushed
// when the pipeline stops, and the registry its metrics are registered with
func newTestPipeline(t *testing.T, amURL string, cfg translationConfig) (*alertPipeline, *prometheus.Registry) {
	t.Helper()

	reg := prometheus.NewRegistry()
	delivery, err := deliveryConfig{discovery: discoveryStatic, urls: []string{amURL}}.build(slog.New(slog.DiscardHandler), reg)
	if err != nil {
		t.Fatalf("build() error = %v", err)
	}

	return newAlertPipeline(newTestTranslator(t, cfg), delivery, 1000, time.Hour, false, slog.New(slog.DiscardHandler), reg), reg
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

	var got []models.PostableAlert
	p, _ := newTestPipeline(t, fakeAlertmanager(t, &got), translationConfig{
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

	if len(got) != 1 {
		t.Fatalf("sent %d alerts, want 1", len(got))
	}
	if got[0].Labels["alarm_id"] != "25" || got[0].Labels["env"] != "prod" || got[0].Labels["instance_name"] != "OpenNMS" {
		t.Errorf("labels = %v, want alarm_id 25, env prod and instance_name OpenNMS", got[0].Labels)
	}
	if endsAt, want := time.Time(got[0].EndsAt), now.Add(time.Minute*5); !endsAt.Equal(want) {
		t.Errorf("EndsAt = %v, want %v", endsAt, want)
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
			var got []models.PostableAlert
			p, reg := newTestPipeline(t, fakeAlertmanager(t, &got), translationConfig{})

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

	var got []models.PostableAlert
	p, _ := newTestPipeline(t, fakeAlertmanager(t, &got), translationConfig{
		extraLabels: map[string]string{"env": "prod"},
	})

	p.heartbeat(pb.HeartBeat_builder{
		MonitoringInstance: pb.MonitoringInstance_builder{
			InstanceId:   "6f8a7c1e-uuid",
			InstanceName: "OpenNMS",
		}.Build(),
		Message: "heartbeat",
	}.Build(), now)

	if len(got) != 1 {
		t.Fatalf("sent %d alerts, want 1", len(got))
	}
	if got[0].Labels["alertname"] != "OpenNMSHeartbeat" || got[0].Labels["env"] != "prod" {
		t.Errorf("labels = %v, want alertname OpenNMSHeartbeat and env prod", got[0].Labels)
	}
	if startsAt := time.Time(got[0].StartsAt); !startsAt.Equal(now) {
		t.Errorf("StartsAt = %v, want %v", startsAt, now)
	}
}
