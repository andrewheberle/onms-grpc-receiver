package server

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// firingKey identifies an alarm in the firing memory. Alarm IDs are only
// unique within a Horizon instance.
type firingKey struct {
	instanceID string
	alarmID    uint64
}

// firingEntry is what the firing memory holds for an alarm
type firingEntry struct {
	firing   firingSeverity
	lastSeen time.Time
}

// firingMemory remembers the Firing severity of each alarm, so that when an
// alarm is cleared or changes severity only the alert it fired with is
// resolved, rather than a Severity fan-out. See ADR 0006.
//
// It is only used by the single worker that flushes alarms, so it is not safe
// for concurrent use.
type firingMemory struct {
	// how long an alarm is remembered after it was last seen
	window time.Duration

	alarms map[firingKey]firingEntry

	// instances whose first snapshot since the receiver started has been
	// handled, after which every alarm sent as firing is remembered
	snapshotted map[string]bool

	// metrics
	remembered *prometheus.GaugeVec
}

// newFiringMemory returns a firingMemory that remembers alarms for window
// after they were last seen, and registers its metrics with reg
func newFiringMemory(window time.Duration, reg prometheus.Registerer) (*firingMemory, error) {
	m := &firingMemory{
		window:      window,
		alarms:      make(map[firingKey]firingEntry),
		snapshotted: make(map[string]bool),
		remembered: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "onmsgrpc_alarm_remembered",
			Help: "Current number of alarms from a Horizon instance whose Firing severity is remembered.",
		},
			[]string{"instance_id"}),
	}
	if err := register(reg, m.remembered); err != nil {
		return nil, err
	}

	return m, nil
}

// lookup returns what is known of the Firing severity of an alarm. An alarm
// that is not remembered is new once the first snapshot from its instance has
// been handled, and unknown before then, as it may have been sent before the
// receiver started.
func (m *firingMemory) lookup(key firingKey) firingSeverity {
	if e, ok := m.alarms[key]; ok {
		return e.firing
	}

	if m.snapshotted[key.instanceID] {
		return firingSeverity{known: true}
	}

	return firingSeverity{}
}

// record remembers what is known of the Firing severity of an alarm seen at
// now
func (m *firingMemory) record(key firingKey, firing firingSeverity, now time.Time) {
	if _, ok := m.alarms[key]; !ok {
		m.remembered.WithLabelValues(key.instanceID).Inc()
	}

	m.alarms[key] = firingEntry{firing: firing, lastSeen: now}
}

// forget forgets an alarm
func (m *firingMemory) forget(key firingKey) {
	if _, ok := m.alarms[key]; !ok {
		return
	}

	delete(m.alarms, key)
	m.remembered.WithLabelValues(key.instanceID).Dec()
}

// markUnknown marks the Firing severity of alarms as unknown, as the alerts
// sent for them may not have reached Alertmanager
func (m *firingMemory) markUnknown(keys []firingKey) {
	for _, key := range keys {
		if e, ok := m.alarms[key]; ok {
			e.firing = firingSeverity{}
			m.alarms[key] = e
		}
	}
}

// reconnected marks the Firing severity of every alarm of an instance as
// unknown, and the first snapshot from it as not yet handled, as it has opened
// a new alarm update stream. While it was not connected to this receiver it
// may have sent alarm updates to another, which this receiver does not know
// were sent.
func (m *firingMemory) reconnected(instanceID string) {
	delete(m.snapshotted, instanceID)
	for key, e := range m.alarms {
		if key.instanceID == instanceID {
			e.firing = firingSeverity{}
			m.alarms[key] = e
		}
	}
}

// snapshotHandled records that a snapshot from an instance has been handled
func (m *firingMemory) snapshotHandled(instanceID string) {
	m.snapshotted[instanceID] = true
}

// prune forgets alarms not seen for the window before now, by when the alert
// for their Firing severity has ended in Alertmanager
func (m *firingMemory) prune(now time.Time) {
	cutoff := now.Add(-m.window)
	for key, e := range m.alarms {
		if e.lastSeen.Before(cutoff) {
			m.forget(key)
		}
	}
}
