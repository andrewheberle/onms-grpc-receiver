# onms-grpc-receiver

Receives Alarms and Heartbeats from OpenNMS Horizon instances over Service Provider over gRPC (SPoG) and forwards them to Alertmanager as Alerts.

## Sources

**Horizon instance**:
A downstream OpenNMS Horizon server that streams its Alarms and Heartbeats to the receiver, identified by an instance ID and name.
_Avoid_: Node, source, client

**Alarm**:
An OpenNMS record of an ongoing problem on a Horizon instance, with a severity and event timestamps.
_Avoid_: Event, alert (an Alert is what the receiver sends)

**Alarm update**:
A message from a Horizon instance carrying one or more of its Alarms, either those that changed or a Snapshot.
_Avoid_: Alarm list, alarm message

**Snapshot**:
An Alarm update containing every current Alarm on a Horizon instance, rather than only the ones that changed.
_Avoid_: Full sync, dump

**Cleared Alarm**:
An Alarm whose severity is Cleared, meaning the problem has been resolved but OpenNMS still holds the record.
_Avoid_: Resolved alarm, closed alarm

**Last update time**:
The later of an Alarm's last event time and its last automation time; for a Cleared Alarm, the best available record of when it was cleared.
_Avoid_: Clear time, modified time

**Operator instructions**:
Free-text guidance OpenNMS attaches to an Alarm from its event definition, which may contain a runbook URL.
_Avoid_: Operinstruct, instructions, notes

**Heartbeat**:
A periodic liveness message from a Horizon instance.
_Avoid_: Ping, keepalive

## Destination

**Alert**:
What the receiver posts to Alertmanager, carrying labels (its identity) and annotations (human-readable detail).
_Avoid_: Alarm, notification

**Built-in label** / **Built-in annotation**:
A label or annotation the receiver sets on Alerts itself, derived from the Alarm or Heartbeat.
_Avoid_: Default label, standard label

**Extra label** / **Extra annotation**:
A label or annotation configured by the operator and added to every Alert; it may not reuse a Built-in name.
_Avoid_: Custom label, static label, global label

**Alertmanager**:
A Prometheus Alertmanager that receives Alerts; there may be several, listed directly or found from a DNS SRV record, and each one is sent every Alert.
_Avoid_: AM, receiver, target

**Alert pipeline**:
Everything that happens to an Alarm update or Heartbeat after it is received, through Alert translation to Alert delivery.
_Avoid_: Handler, processor, worker

**Alert delivery**:
Sending Alerts to every Alertmanager currently known.
_Avoid_: Forwarding, dispatch, notification

**Alert translation**:
The rules that turn an Alarm or Heartbeat from a Horizon instance into an Alert, or decide it is not sent.
_Avoid_: Conversion, mapping, alert builder

**Runbook URL template**:
An operator-configured pattern that builds an Alert's runbook URL from its labels, used when the Alarm's Operator instructions contain no URL.
_Avoid_: Runbook default, runbook fallback, runbook pattern
