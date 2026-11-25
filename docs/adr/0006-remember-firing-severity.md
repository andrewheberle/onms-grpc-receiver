# Remember each Alarm's Firing severity

ADR 0003 sent a Cleared Alarm as a resolved Alert for every Alert severity, and a firing Alarm updated within the cleared Alarm window as a resolved Alert for every other Alert severity, on the basis that Alertmanager does not notify for an Alert that never fired. It only holds back a notification for an alert group that has never notified and holds only resolved Alerts. With a `group_by` that does not include `severity`, every Alert severity of an Alarm shares a group with the Alert that fired, so its notifications listed up to four resolved Alerts that never fired, including the first notification of every new Alarm. The receiver now remembers the Firing severity of each Alarm, keyed on instance ID and Alarm ID, so a severity change or clear resolves only the Alert that fired, and a new Alarm is sent as its firing Alert alone. The memory only records what this receiver has sent, so it falls back to the Severity fan-out of ADR 0003 whenever that may not be what Alertmanager holds: before the first Snapshot from a Horizon instance since the receiver started, after Alert delivery of an Alarm fails, and after a Horizon instance opens a new Alarm update stream, as it may have sent Alarm updates to another receiver while not connected to this one.

## Considered Options

- **Documenting `severity` in `group_by`**: keeps the receiver stateless, but splits each Alarm's notifications by severity and leaves the extra resolved Alerts for anyone who groups otherwise, with nothing in a notification template to tell them apart.
- **Asking Alertmanager which Alert severity is firing**: keeps the receiver stateless, but adds a request before every send, and with several Alertmanagers each may answer differently.
- **Moving severity to an annotation**: rejected in ADR 0003, as it breaks every route, inhibit rule and silence that matches on `severity`.
- **Expiring the first Snapshot once Snapshots stop, rather than resetting on a new stream**: needs nothing from the gRPC handler, but misses a short move to another receiver and back.

## Consequences

- The memory is held in process only. For about one Snapshot interval after the receiver starts or a Horizon instance reconnects, notifications may still list resolved Alerts that never fired.
- An Alarm is forgotten once it has not been seen for the resolve timeout plus the Resolve hold, by when the Alert for its Firing severity has ended in Alertmanager. A firing Alarm is in every Snapshot, so is only forgotten when Snapshots stop.
- Two Horizon instances built from copies of one database share an instance ID, and may have Alarms with the same ID. One then overwrites the Firing severity remembered for the other's Alarm, so a clear can resolve the wrong Alert severity and leave the Alert that fired until it ends. The README warns of this.
- Repeated Alarm updates that do not change severity no longer send resolved Alerts, and a Cleared Alarm with a known Firing severity is sent as one resolved Alert rather than five.
