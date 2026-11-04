# OpenNMS gRPC Receiver

[![codecov](https://codecov.io/gh/andrewheberle/onms-grpc-receiver/graph/badge.svg?token=VSHIGURNMQ)](https://codecov.io/gh/andrewheberle/onms-grpc-receiver)
[![GitHub version](https://badge.fury.io/gh/andrewheberle%2Fonms-grpc-receiver.svg?icon=si%3Agithub)](https://badge.fury.io/gh/andrewheberle%2Fonms-grpc-receiver)

This is a service that acts as a server for the [OpenNMS gRPC Exporter](https://docs.opennms.com/horizon/33/operation/deep-dive/grpc-exporter/grpc-exporter.html) and forwards alerts to the configured Altermanager instance(s).

## Running via Docker

```sh
docker run -p 8080:8080 ghcr.io/andrewheberle/onms-grpc-receiver:v0.13.2 --alertmanager.url http://am:9092 # x-release-please-version
```

## Command Line Options

| Flag                  | Decription                                                 | Default        |
|-----------------------|------------------------------------------------------------|----------------|
| --address             | Service gRPC listen address                                | localhost:8080 |
| --alert.annotations   | Annotations added to all alerts                            |                |
| --alert.labels        | Labels added to all alerts                                 |                |
| --alert.runbook-url   | Template for alerts without a runbook URL                  |                |
| --alert.severity      | Map OpenNMS severities to alert severities (see below)     |                |
| --alertmanager.scheme | Alertmanager scheme (http/https) when SRV records are used | http           |
| --alertmanager.srv    | Alertmanager SRV Record                                    |                |
| --alertmanager.url    | Alertmanager URL                                           |                |
| --cert                | TLS Certificate                                            |                |
| --debug               | Enable debug logging                                       |                |
| --grpc.max-recv-size  | Max gRPC message size in bytes (must be greater than zero) | 67108864       |
| --headers             | Headers added to requests to Alertmanager (see below)      |                |
| --key                 | TLS Key                                                    |                |
| --map.url             | Map Horizon instance ID's to URLs                          |                |
| --metrics.address     | Metrics listen address                                     |                |
| --metrics.path        | Metrics path                                               | /metrics       |
| --silent              | Disable all logging                                        |                |
| --verbose             | Log all messages                                           |                |
| --resolve.timeout     | Resolve timeout for alarms (must be greater than zero)     | 5m             |
| --resolve.hold        | Extra time firing alarm alerts stay active (see below)     | 0s             |
| --srv.ttl             | TTL for cached SRV lookups (`0` disables caching)          | 30s            |

Options are checked when the receiver starts, and it will not start if any are
invalid, listing every problem found so they can all be fixed at once.

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

The above options are mutually exclusive. Only the options are checked on
startup, not that any Alertmanager is reachable. The receiver will not start
if:

* An Alertmanager URL is not an `http` or `https` URL with a host, such as
  `am:9093` without a scheme, or has a fragment (`#...`). A path prefix,
  credentials (`http://user:password@am:9093`) and a query string are allowed.
* The same Alertmanager URL is listed more than once (`http://am:9093` and
  `http://am:9093/` are the same).
* `--alertmanager.scheme` is not `http` or `https`.
* `--srv.ttl` is negative.

If neither option is set, alarms are only logged and nothing is sent, and a
warning is logged on startup.

As credentials and query strings may be secret, an Alertmanager URL is only
ever shown as `scheme://host[:port][/path]` in errors, logs and the
`alertmanager` label of metrics. For example
`https://noc:secret@am:9093/prefix?token=abc` is shown as
`https://am:9093/prefix/api/v2/alerts`, while requests are still sent with the
credentials and query string.

Headers may be added to every request to Alertmanager, such as for
authentication or a multi-tenant proxy:

```sh
onms-grpc-receiver spog --alertmanager.url http://am:9093 --headers X-Scope-OrgID=noc
```

The receiver will not start if a header name is not a valid HTTP header name, a
value has a control character such as a newline, or the header is `Host`,
`Content-Type` or `Content-Length`, which would be ignored or break the request.
Header values are never included in errors.

### Delivery and Retries

Every Alertmanager is sent every alert, as the members of an Alertmanager
cluster do not share alerts with each other. Large updates, such as alarm
snapshots, are sent in requests of up to 1000 alerts.

A request that fails with a network error, a timeout (5 seconds), a `5xx`
status or `429 Too Many Requests` is retried up to twice, after 500ms and then
1s. Other responses, such as `400 Bad Request` when some alerts are invalid, are
not retried, as Alertmanager has already stored the valid alerts, and the error
from Alertmanager is logged. Sending to all Alertmanagers, including any
retries, gives up after 10 seconds, so that an Alertmanager that is down does
not hold up the alarms and heartbeats that follow.

Alerts that could not be sent are not kept to be sent later. Firing alarms are
sent again with the next alarm snapshot (every 2 minutes by default), and
cleared alarms are sent with every snapshot until the resolve timeout plus any
[hold](#handling-opennms-restarts) has passed since they were cleared.

When the receiver is stopped, it has up to 5 seconds to send the alarms it has
already received, so stopping takes at most about 8 seconds, within the 10
second default of `docker stop`.

Delivery may be monitored with the following metrics, by `alertmanager`, which
is the URL alerts are posted to without any credentials or query string:

| Metric                                | Description                                                |
|---------------------------------------|------------------------------------------------------------|
| `onmsgrpc_alertmanager_total`         | Sends of alerts to an Alertmanager                         |
| `onmsgrpc_alertmanager_failed_total`  | Sends to an Alertmanager that failed, after any retries    |
| `onmsgrpc_alertmanager_retries_total` | Requests to an Alertmanager that were retried              |

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

As `severity` is a label, it is part of the identity of an alert. When an
alarm is cleared or changes severity, the receiver does not know which severity
its alert was firing with, so it also sends the alert as resolved for every
other severity (at the time the alarm was last updated). The previous alert is
resolved straight away, and Alertmanager does not notify for the others as they
never fired. A cleared alarm is only sent as these resolved alerts, so no alert
has a `severity` of `cleared`.

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

### Severity Map

By default the `severity` label is the lowercased name of the OpenNMS severity
(`indeterminate`, `warning`, `minor`, `major` or `critical`). To
match the severities your Alertmanager routes and inhibit rules expect, map
OpenNMS severities to other values as follows:

```sh
onms-grpc-receiver spog --alert.severity critical=P1,major=P2,minor=P3
```

Or via an environment variable:

```sh
export ONMS_GRPC_ALERT_SEVERITY="critical=P1,major=P2,minor=P3"
```

Severity names are case-insensitive, and any severity not in the map keeps its
default value. The runbook URL template sees the mapped value in
`{{ .Labels.severity }}`.

The receiver will not start if a mapping:

* Is for a severity that is not `indeterminate`, `warning`, `minor`, `major` or
  `critical`. `normal` alarms are never sent and `cleared` is used to end
  alerts, so neither can be mapped.
* Has an empty value.

Several severities may be mapped to the same value (for example
`major=page,critical=page`), so changes between them keep the same alert.
Changing the map turns every affected firing alert into a new alert, with the
previous alert expiring after the resolve timeout plus any
[hold](#handling-opennms-restarts).

### Alarm link/URL

The direct linking of an alarm in Alertmanager to OpenNMS is handled by providing a mapping of the Horizon instance to a base URL as follows:

```sh
onms-grpc-receiver spog --map.url "uuid-of-horizon-instance=http://horizon:8980/opennms/"
```

Based on the above an alert from `uuid-of-horizon-instance` with an alert ID `25` would result in a URL of `http://horizon:8980/opennms/alarm/detail.htm?id=25`

The receiver will not start if an instance ID is empty, or a URL:

* Is not an `http` or `https` URL with a host.
* Has credentials (`http://user:password@horizon:8980/opennms/`), as the URL
  is a link that people open from an alert, and OpenNMS does not accept
  credentials in it.
* Has a query string or fragment, as the alarm ID is added as the query
  string.

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

Enabling metrics also enables the `/-/healthy` and `/-/ready` health checks
(see [Health Checks](#health-checks)).

The receiver will not start if `--metrics.path` does not start with `/`,
contains whitespace, `{` or `}`, or is `/-/healthy` or `/-/ready`.

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

## Health Checks

The receiver reports whether it is ready to receive alarm updates and
heartbeats from Horizon instances. It is only ready while it is serving gRPC:
not while it is starting, and not from the moment it starts stopping, so load
balancers and Kubernetes stop sending new connections to it straight away.

| Check                      | Where                          | Responds                                                       |
|----------------------------|--------------------------------|----------------------------------------------------------------|
| `/-/healthy`               | Metrics listener               | `200 OK` whenever the receiver is running (liveness)           |
| `/-/ready`                 | Metrics listener               | `200 OK` while ready, otherwise `503 Service Unavailable`      |
| `grpc.health.v1.Health`    | gRPC listener (`--address`)    | `SERVING` while ready, otherwise `NOT_SERVING`                 |

The HTTP checks are only served when `--metrics.address` is set, which the
container image does by default. The gRPC health service reports the same
status for the server as a whole (an empty service name) and for
`org.opennms.plugin.grpc.proto.spog.NmsInventoryServiceSync`.

Readiness does not depend on whether Alertmanager can be reached. Alerts that
cannot be sent are retried, and the next alarm snapshot repairs anything
missed, while refusing Horizon instances would lose more alarm updates, and
would make every receiver unready at the same time.

In Kubernetes, use the HTTP checks, which also work when `--cert` and `--key`
are set (with `scheme: HTTPS`), as Kubernetes gRPC probes do not support TLS:

```yaml
livenessProbe:
  httpGet:
    path: /-/healthy
    port: 8081
readinessProbe:
  httpGet:
    path: /-/ready
    port: 8081
  periodSeconds: 5
```

### Docker

The container image has a `HEALTHCHECK` that runs the `healthcheck` command,
which asks the gRPC health service of the receiver whether it is serving. It
exits with a status of 0 and prints `SERVING` when it is, and otherwise exits
with a status of 1 and prints why. The check runs every 30 seconds, and the
container is marked unhealthy after 3 failed checks in a row, not counting
those in the first 15 seconds.

This lets Docker Compose wait for the receiver before starting services that
depend on it, and Docker Swarm replace an unhealthy receiver:

```yaml
services:
  onms-grpc-receiver:
    image: ghcr.io/andrewheberle/onms-grpc-receiver:v0.13.2 # x-release-please-version
    command: --alertmanager.url http://alertmanager:9093
  app:
    image: example/app
    depends_on:
      onms-grpc-receiver:
        condition: service_healthy
```

The `healthcheck` command reads the same `ONMS_GRPC_` environment variables as
`spog`, so it checks the receiver the container runs without any arguments:

| Flag      | Decription                                                       | Default        |
|-----------|------------------------------------------------------------------|----------------|
| --address | Service gRPC listen address of the receiver to check             | localhost:8080 |
| --cert    | Connect with TLS, without verifying the certificate, when set    |                |
| --timeout | How long the check may take                                      | 3s             |

An address with no host or an unspecified one, such as `:8080` or
`0.0.0.0:8080`, is checked on `localhost`. When the receiver uses TLS, the
check connects over TLS but does not verify the receiver's certificate, as
the names in it cannot match `localhost`, and only the receiver's health is
asked for.

Kubernetes ignores `HEALTHCHECK`, so use the probes above, and Podman ignores
it for images in the OCI format.

## High Availability

The receiver keeps no state of its own: what is firing is held by
Alertmanager, and every alarm snapshot from OpenNMS repairs anything missed.
So several receivers can run side by side, active/active, behind a TCP load
balancer or a Kubernetes Service, with nothing shared between them.

* Each Horizon instance connects to one receiver at a time, as its gRPC
  exporter sends everything over a single connection to the host it is
  configured with.
* Configure every receiver with the same Alertmanagers and the same options,
  so an alarm is sent as the same alert whichever receiver it reaches.
* Use the [readiness checks](#health-checks), so a receiver that is starting
  or stopping is not sent new connections.

When a receiver stops or fails, the Horizon instances connected to it
reconnect to another within about 30 seconds, which is how often their
exporter retries. Alarm updates and heartbeats sent while disconnected are
lost, so alarms are only correct again with the next alarm snapshot (every 2
minutes by default). An alert must therefore stay active for at least about
2½ minutes after it is last sent, so `--resolve.timeout` plus any
`--resolve.hold` should be at least 3 minutes. The default resolve timeout of
5 minutes is enough.

An alarm update queued on a receiver as it stops can reach Alertmanager after
a newer update for the same alarm sent through another receiver. If that
update cleared the alarm, the alert fires again until the next snapshot
resolves it, within about 2 minutes.

Receivers do not close connections to spread them evenly, such as after
adding a receiver, as each reconnect loses up to a minute of alarm updates
from that Horizon instance. Connections move when receivers are restarted, as
in a rolling update.

A connection with nothing received for 2 minutes is pinged, and closed if the
ping is not answered within 20 seconds, as Horizon instances send no pings of
their own. As each Horizon instance sends a heartbeat every minute, this only
happens when an instance or the network to it has gone away, so its connection
is not held open until TCP gives up.
