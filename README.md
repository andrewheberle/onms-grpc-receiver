# OpenNMS gRPC Receiver

[![codecov](https://codecov.io/gh/andrewheberle/onms-grpc-receiver/graph/badge.svg?token=VSHIGURNMQ)](https://codecov.io/gh/andrewheberle/onms-grpc-receiver)
[![GitHub version](https://badge.fury.io/gh/andrewheberle%2Fonms-grpc-receiver.svg?icon=si%3Agithub)](https://badge.fury.io/gh/andrewheberle%2Fonms-grpc-receiver)

This is a service that acts as a server for the [OpenNMS gRPC Exporter](https://docs.opennms.com/horizon/33/operation/deep-dive/grpc-exporter/grpc-exporter.html) and forwards alerts to the configured Altermanager instance(s).

## Running via Docker

```sh
docker run -p 8080:8080 ghcr.io/andrewheberle/onms-grpc-receiver:v0.12.0 --alertmanager.url http://am:9092 # x-release-please-version
```

## Command Line Options

| Flag                  | Decription                                                 | Default        |
|-----------------------|------------------------------------------------------------|----------------|
| --address             | Service gRPC listen address                                | localhost:8080 |
| --alert.annotations   | Annotations added to all alerts                            |                |
| --alert.labels        | Labels added to all alerts                                 |                |
| --alert.runbook-url   | Template for alerts without a runbook URL                  |                |
| --alertmanager.scheme | Alertmanager scheme (http/https) when SRV records are used | http           |
| --alertmanager.srv    | Alertmanager SRV Record                                    |                |
| --alertmanager.url    | Alertmanager URL                                           |                |
| --cert                | TLS Certificate                                            |                |
| --debug               | Enable debug logging                                       |                |
| --grpc.max-recv-size  | Maximum gRPC message size in bytes that can be received    | 67108864       |
| --headers             | Custom headers                                             |                |
| --key                 | TLS Key                                                    |                |
| --map.url             | Map Horizon instance ID's to URLs                          |                |
| --metrics.address     | Metrics listen address                                     |                |
| --metrics.path        | Metrics path                                               | /metrics       |
| --silent              | Disable all logging                                        |                |
| --verbose             | Log all messages                                           |                |
| --resolve.timeout     | Resolve timeout for alarms (must be greater than zero)     | 5m             |
| --resolve.hold        | Extra time firing alarm alerts stay active (see below)     | 0s             |
| --srv.ttl             | TTL for cached SRV lookups                                 | 30s            |

All command line options may also be provided as environment variables with the prefix of `ONMS_GRPC` as follows:

```sh
export ONMS_GRPC_DEBUG="true"
export ONMS_GRPC_ALERTMANAGER_URL="http://am-0:9091,http://am-1:9091"
onms-grpc-receiver spog
```

The container build sets the following environment variables by default:

* `ONMS_GRPC_ADDRESS=":8080"`
* `ONMS_GRPC_METRICS_ADDRESS=":8081"`

## Alertmanager Integration

There is a basic implementation of sending data to a downstream Alertmanager
instance/cluster.

This process queues incoming alarms and then sends them to Alertmanager(s)
after a short delay (20s) or the number of alarms hits a threshold (10).

You may either specify via one or more `--alertmanager.url` as follows:

```sh
onms-grpc-receiver spog --alertmanager.url http://am-0:9091 --alertmanager.url http://am-1:9091 
```

Or you may use SRV record lookups using the `--alertmanager.srv` and optionally
`--alertmanager.scheme` as follows:

```sh
onms-grpc-receiver spog --alertmanager.srv _http.alertmanager --alertmanager.scheme http
```

The above options are mutually exclusive, in addition only basic validation of
provided URLs is done, not that any Alertmanager is reachable on startup.

### Alert Names and Labels

The alert name sent to Alertmanager is the OpenNMS "uei" value such as
"uei.opennms.org/nodes/nodeDown" or "uei.opennms.org/nodes/dataCollectionFailed".

Labels are set as follows:

| OpenNMS Alarm Field                      | Alertmanager Label |                                         |
|------------------------------------------|--------------------|-----------------------------------------|
| UEI of Alarm                             | alertname          |                                         |
| Alarm ID                                 | alarm_id           |                                         |
| Node Name                                | node_name          |                                         |
| Node Foreign Source (requisition)        | foreign_source     | Only present for provisioned nodes      |
| Instance Name (name of Horizon instance) | instance_name      |                                         |
| Severity                                 | severity           |                                         |
| Service (name)                           | service            | Only present on service outages         |
| Interface (IP address)                   | ip_address         | Only present on alarms with an interface |
| Node Location                            | site               |                                         |

The instance name is the `org.opennms.instance.id` system property of the
Horizon instance, which defaults to `OpenNMS`. When forwarding alarms from more
than one Horizon instance, give each a unique instance name, as alarm ID's are
only unique within an instance.

### Annotations

Human readable details of the alarm are added as annotations, with any HTML
converted to plain text:

| OpenNMS Alarm Field   | Alertmanager Annotation | Notes                                                      |
|-----------------------|-------------------------|------------------------------------------------------------|
| Log Message           | summary                 | Only present if set                                        |
| Description           | description             | Only present if set                                        |
| Operator Instructions | runbook_url             | The first http(s) URL in the operator instructions, if any |
| Operator Instructions | operator_instructions   | Only present if set to more than just a URL                |

The operator instructions of an event may be used to provide a runbook for
alarms of that type. For example, with the following in the event definition
the alert has a `runbook_url` annotation of
`https://wiki.example.com/runbooks/node-down` and no `operator_instructions`
annotation:

```xml
<operinstruct>https://wiki.example.com/runbooks/node-down</operinstruct>
```

When the operator instructions contain other text, such as
`Check the router, see https://wiki.example.com/runbooks/node-down`, the URL is
used as the `runbook_url` annotation and the full text as the
`operator_instructions` annotation. Links in HTML, such as
`<a href="https://wiki.example.com/runbooks/node-down">the runbook</a>`, are
used in preference to URLs in the text.

### Runbook URL Template

As stock OpenNMS events have no operator instructions, a template may be set
with `--alert.runbook-url` (or `ONMS_GRPC_ALERT_RUNBOOK_URL`) to give alerts a
`runbook_url` annotation when their operator instructions contain no URL. A URL
in the operator instructions is always used in preference to the template.

The template is a Go [text/template](https://pkg.go.dev/text/template) and is
given the labels of the alert, including any additional labels, as `.Labels`:

```sh
onms-grpc-receiver spog --alert.runbook-url 'https://runbooks.example.com/{{ .Labels.alertname }}'
```

The `alertname` of an alarm is its UEI, so the above results in a `runbook_url`
of `https://runbooks.example.com/uei.opennms.org/nodes/nodeDown` for a node
down alarm. The output is not escaped, so use the `pathEscape` function to use
a label as a single path segment, giving
`https://runbooks.example.com/uei.opennms.org%2Fnodes%2FnodeDown`:

```sh
onms-grpc-receiver spog --alert.runbook-url 'https://runbooks.example.com/{{ pathEscape .Labels.alertname }}'
```

The template is also used for heartbeat alerts, which have an `alertname` of
`OpenNMSHeartbeat`.

Labels that are not set on an alert are empty, and no `runbook_url` annotation
is added if the template results in an empty string. The receiver will not
start if the template is invalid, such as when it refers to `.Lables` rather
than `.Labels`.

### Additional Labels and Annotations

Labels and annotations may be added to all alerts, including the
`OpenNMSHeartbeat` alert, as follows:

```sh
onms-grpc-receiver spog --alert.labels env=prod,team=noc --alert.annotations dashboard=https://grafana/d/onms
```

Or via environment variables:

```sh
export ONMS_GRPC_ALERT_LABELS="env=prod,team=noc"
export ONMS_GRPC_ALERT_ANNOTATIONS="dashboard=https://grafana/d/onms"
```

Entries are separated by commas, so an entry whose value contains a comma must
be quoted as a whole:

```sh
onms-grpc-receiver spog --alert.annotations '"note=Call the NOC, then escalate",dashboard=https://grafana/d/onms'
```

The receiver will not start if a label or annotation:

* Has the same name as a built-in label or annotation listed above, even those
  that are only set on some alerts, such as `service` or `runbook_url`.
  Names are case-sensitive, so `Severity` does not clash with `severity`.
* Has a name that is not a valid Prometheus label name (`[a-zA-Z_][a-zA-Z0-9_]*`)
  or starts with `__`, which is reserved.
* Has an empty value.

Labels form the identity of an alert in Alertmanager, so adding, removing or
changing an additional label turns every firing alert into a new alert, with the
previous alert expiring after the resolve timeout (5 minutes by default) plus
any [hold](#handling-opennms-restarts). Changes to additional annotations do
not.

### Alarm link/URL

The direct linking of an alarm in Alertmanager to OpenNMS is handled by providing a mapping of the Horizon instance to a base URL as follows:

```sh
onms-grpc-receiver spog --map.url "uuid-of-horizon-instance=http://horizon:8980/opennms/"
```

Based on the above an alert from `uuid-of-horizon-instance` with an alert ID `25` would result in a URL of `http://horizon:8980/opennms/alarm/detail.htm?id=25`

### Handling OpenNMS restarts

OpenNMS sends a snapshot of every alarm every 2 minutes (set by the
`org.opennms.alarms.snapshot.sync.ms` system property in OpenNMS), and each
firing alarm alert stays active in Alertmanager for the resolve timeout after it
was last received. While OpenNMS restarts no snapshots are sent, so with the
default 5 minute resolve timeout a restart that takes more than about 3 minutes
resolves every firing alert. Once OpenNMS is back they all fire again, which
can mean a large wave of resolved and re-opened tickets, messages or calls in
the integrations downstream of Alertmanager.

The `--resolve.hold` option adds extra time to firing alarm alerts, so they
stay active as long as the gap between the last snapshot before OpenNMS stops
and the first snapshot after it starts is shorter than the resolve timeout plus
the hold:

```sh
onms-grpc-receiver spog --resolve.hold 10m
```

A hold of 5 to 10 minutes is a reasonable starting point, but you should review
it against how long your OpenNMS instances take to restart. The first snapshot
arrives a few minutes after OpenNMS has started, so the gap is longer than the
restart itself, and it can be measured from the
`onmsgrpc_alarm_last_snapshot_timestamp_seconds` metric (see
[Alarm snapshots](#alarm-snapshots)).

The hold only applies to firing alarm alerts:

* Cleared alarms still resolve their alert at the time they were cleared,
  including those cleared while OpenNMS was restarting, which are sent with the
  first snapshot after it starts.
* The `OpenNMSHeartbeat` alert still resolves the resolve timeout after the last
  heartbeat, so it shows OpenNMS is down while alarm alerts are being held.

The hold is applied at all times, not only during a restart. OpenNMS does not
send anything when an alarm is deleted, so an alarm deleted without first being
cleared resolves its alert the resolve timeout plus the hold after the last
snapshot that included it.

## Metrics

Prometheus metrics are exposed on the `/metrics` path (by default) when the `--metrics.address` flag is provided.

Enabling metrics also enables a health check endpoint at `/-/healthy` that responds with `200 OK`.

### Alarm snapshots

OpenNMS periodically sends a full snapshot of all active alarms, which keeps alerts active in Alertmanager. If snapshots stop arriving, alerts will age out of Alertmanager even though the alarms are still active in OpenNMS.

The `onmsgrpc_alarm_last_snapshot_timestamp_seconds` gauge records the Unix time of the last snapshot received from each Horizon instance (by `instance_id`). The series only exists once a snapshot has been received, so alert both on stale snapshots and on instances that are sending alarm updates but have never sent a snapshot:

```yaml
groups:
  - name: onms-grpc-receiver
    rules:
      - alert: OpenNMSAlarmSnapshotStale
        expr: time() - onmsgrpc_alarm_last_snapshot_timestamp_seconds > 900
        for: 5m
      - alert: OpenNMSAlarmSnapshotMissing
        expr: onmsgrpc_alarm_total unless on(instance_id) onmsgrpc_alarm_last_snapshot_timestamp_seconds
        for: 15m
```

The second rule also covers the period after the receiver restarts, until the next snapshot arrives.
