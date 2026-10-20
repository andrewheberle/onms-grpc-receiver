# OpenNMS gRPC Receiver

[![codecov](https://codecov.io/gh/andrewheberle/onms-grpc-receiver/graph/badge.svg?token=VSHIGURNMQ)](https://codecov.io/gh/andrewheberle/onms-grpc-receiver)

This is a service that acts as a server for the [OpenNMS gRPC Exporter](https://docs.opennms.com/horizon/33/operation/deep-dive/grpc-exporter/grpc-exporter.html) and forwards alerts to the configured Altermanager instance(s).

## Running via Docker

```sh
docker run -p 8080:8080 ghcr.io/andrewheberle/onms-grpc-receiver:latest --alertmanager.url http://am:9092
```

## Command Line Options

| Flag                  | Decription                                                 | Default        |
|-----------------------|------------------------------------------------------------|----------------|
| --address             | Service gRPC listen address                                | localhost:8080 |
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
| --resolve.timeout     | Resolve timeout for alarms                                 | 5m             |
| --srv.ttl             | TTL for cached SRV lookups                                 | 30s            |

All command line options may also be provided as environment variables with the prefix of `ONMS_GRPC` as follows:

```sh
export ONMS_GRPC_DEBUG="true"
export ONMS_GRPC_ALERTMANAGER_URL="http://am-0:9091,http://am-1:9091"
onms-grpc-receiver spog
```

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

### Alarm link/URL

The direct linking of an alarm in Alertmanager to OpenNMS is handled by providing a mapping of the Horizon instance to a base URL as follows:

```sh
onms-grpc-receiver spog --map.url "uuid-of-horizon-instance=http://horizon:8980/opennms/"
```

Based on the above an alert from `uuid-of-horizon-instance` with an alert ID `25` would result in a URL of `http://horizon:8980/opennms/alarm/detail.htm?id=25`

## Metrics

Prometheus metrics are exposed on the `/metrics` path (by default) when the `--metrics.address` flag is provided.

Enabling metrics also enables a health check endpoint at `/-/healthy` that responds with `200 OK`.
