# Bounded per-Alertmanager retries in Alert delivery

Alert delivery retries each Alertmanager on its own, and only after a network error, a 5xx or a 429, making three requests in all with a backoff of 500ms then 1s. Each request is capped at 5 seconds and the whole send, covering every chunk, retry and Alertmanager, at 10 seconds. The cap comes from where sends run: a single worker flushes Alarms, so a long send holds up the queue of Alarm updates, which drops updates once full, and Heartbeat Alerts are sent from their gRPC stream. A 400 is not retried, as Alertmanager stores the valid Alerts of a request before rejecting it for the invalid ones. Once the receiver is stopped, flushes have 5 seconds to finish, so together with the 3 second gRPC graceful stop, stopping fits within the 10 second default grace period of `docker stop`.

## Considered Options

- **Retrying until accepted, or for much longer**: during an Alertmanager outage this holds up the queue of Alarm updates until updates are dropped, Cleared Alarms included, rather than merely delayed, and delays Heartbeat Alerts.
- **Spooling unsent Alerts to disk**: survives a receiver restart, but makes the receiver stateful, against ADR 0002, and complicates running several receivers.
- **Retrying off the flushing worker**: needs a queue per Alertmanager, with the same memory and ordering questions as spooling, only in memory.
- **Sending a whole Snapshot in one request**: with up to five Alerts per Cleared Alarm (ADR 0003), a large Snapshot risks the request timeout, so sends are split into chunks of 1000 Alerts, and a chunk that is not accepted stops the rest for that Alertmanager.

## Consequences

Alerts that are not accepted once retries are used up are not sent again by the receiver; the Snapshots that follow repair them. A firing Alarm is sent with the next Snapshot, and a Cleared Alarm with every Snapshot within the cleared Alarm window, so an Alertmanager outage delays them rather than losing them. If an outage outlasts the window, the Alert for the Cleared Alarm instead resolves by reaching its end time, which is no later than the end of the window. A Heartbeat Alert that is not accepted is sent again with the next Heartbeat.
