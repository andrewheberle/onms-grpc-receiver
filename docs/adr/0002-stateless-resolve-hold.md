# Stateless resolve hold, applied at all times

While a Horizon instance restarts it sends no Snapshots, so Alerts for firing Alarms reach their end time and resolve, then fire again once Snapshots resume, causing a wave of resolved and re-opened notifications downstream of Alertmanager. The optional Resolve hold (`--resolve.hold`, off by default) adds a fixed extra time to the end of every Alert for a firing Alarm, at all times. The receiver keeps no record of the Alerts it has sent. Heartbeat Alerts and Cleared Alarms are not held, and the window in which Cleared Alarms are still sent is the resolve timeout plus the hold, so a Cleared Alarm is always sent while an Alert for it may still be firing. (A Cleared Alarm only resolves the Alert that was firing since ADR 0003, as `severity` is part of the Alert's identity.)

## Considered Options

- **Raising `--resolve.timeout`**: also delays the `OpenNMSHeartbeat` Alert resolving, which is what shows that OpenNMS is down while Alerts are held.
- **Holding only while Heartbeats are missing**: Heartbeats resume before Snapshots after a restart, as Horizon sends Heartbeats on its own 60 second timer while the first Snapshot only follows on a later 2 minute alarmd tick, so the hold would end before Alerts are refreshed.
- **Holding only while Snapshots are overdue (stateful)**: remembers the last firing Alerts per Horizon instance and re-sends them with a later end time until a Snapshot arrives or the hold runs out. Alerts behave as before while healthy, but it needs a refresh loop, memory for every Alarm, state keyed on instance name as well as ID (the ID defaults to all zeros on every Horizon instance), and the state is lost if the receiver restarts during the outage.
- **Resolving Alarms missing from a Snapshot (stateful)**: would allow a long hold without delaying the resolution of Alarms deleted in OpenNMS, but needs the same state as the previous option.

## Consequences

Horizon does not send deleted Alarms, so an Alarm deleted without first being Cleared resolves its Alert the resolve timeout plus the hold after the last Snapshot that included it, even while OpenNMS is healthy. This was judged acceptable as such deletions are rare. If they become common, the stateful options are the way forward.
