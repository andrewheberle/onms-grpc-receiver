package server

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TestFiringMemoryLookup checks what is known of the Firing severity of an
// alarm as it is remembered, marked unknown and forgotten, and once the first
// snapshot from its instance has been handled
func TestFiringMemoryLookup(t *testing.T) {
	now := time.Now()
	key := firingKey{instanceID: "a", alarmID: 1}
	major := firingSeverity{known: true, severity: "major"}

	tests := []struct {
		name   string
		update func(m *firingMemory)
		want   firingSeverity
	}{
		{"not remembered before a snapshot", func(m *firingMemory) {}, firingSeverity{}},
		{"not remembered after a snapshot", func(m *firingMemory) {
			m.snapshotHandled("a")
		}, firingSeverity{known: true}},
		{"not remembered after a snapshot from another instance", func(m *firingMemory) {
			m.snapshotHandled("b")
		}, firingSeverity{}},
		{"remembered", func(m *firingMemory) {
			m.record(key, major, now)
		}, major},
		{"remembered for another instance", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.record(firingKey{instanceID: "b", alarmID: 1}, major, now)
		}, firingSeverity{known: true}},
		{"marked unknown", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.record(key, major, now)
			m.markUnknown([]firingKey{key})
		}, firingSeverity{}},
		{"marked unknown when not remembered", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.markUnknown([]firingKey{key})
		}, firingSeverity{known: true}},
		{"reconnected", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.record(key, major, now)
			m.reconnected("a")
		}, firingSeverity{}},
		{"not remembered after reconnecting", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.reconnected("a")
		}, firingSeverity{}},
		{"snapshot after reconnecting", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.reconnected("a")
			m.snapshotHandled("a")
		}, firingSeverity{known: true}},
		{"another instance reconnected", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.record(key, major, now)
			m.reconnected("b")
		}, major},
		{"forgotten", func(m *firingMemory) {
			m.snapshotHandled("a")
			m.record(key, major, now)
			m.forget(key)
		}, firingSeverity{known: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := must(newFiringMemory(time.Minute*5, prometheus.NewRegistry()))(t)

			tt.update(m)

			if got := m.lookup(key); got != tt.want {
				t.Errorf("lookup() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestFiringMemoryPrune checks alarms are forgotten once they have not been
// seen for the window, and the number remembered per instance is kept
func TestFiringMemoryPrune(t *testing.T) {
	now := time.Now()
	major := firingSeverity{known: true, severity: "major"}

	reg := prometheus.NewRegistry()
	m := must(newFiringMemory(time.Minute*5, reg))(t)

	m.record(firingKey{instanceID: "a", alarmID: 1}, major, now.Add(-time.Minute*6))
	m.record(firingKey{instanceID: "a", alarmID: 2}, major, now.Add(-time.Minute*5))
	m.record(firingKey{instanceID: "b", alarmID: 1}, major, now.Add(-time.Minute*10))
	m.record(firingKey{instanceID: "b", alarmID: 2}, major, now)

	// seen again, so not counted twice
	m.record(firingKey{instanceID: "b", alarmID: 2}, major, now)

	for id, want := range map[string]float64{"a": 2, "b": 2} {
		if got := metricValue(t, reg, "onmsgrpc_alarm_remembered", "instance_id", id); got != want {
			t.Errorf("remembered for %q before prune = %v, want %v", id, got, want)
		}
	}

	m.prune(now)

	for key, want := range map[firingKey]bool{
		{instanceID: "a", alarmID: 1}: false,
		{instanceID: "a", alarmID: 2}: true,
		{instanceID: "b", alarmID: 1}: false,
		{instanceID: "b", alarmID: 2}: true,
	} {
		if _, got := m.alarms[key]; got != want {
			t.Errorf("%+v remembered = %v, want %v", key, got, want)
		}
	}

	for id, want := range map[string]float64{"a": 1, "b": 1} {
		if got := metricValue(t, reg, "onmsgrpc_alarm_remembered", "instance_id", id); got != want {
			t.Errorf("remembered for %q after prune = %v, want %v", id, got, want)
		}
	}
}
