# OpenNMS gRPC Receiver

This is a service that acts as a receiver for the [OpenNMS gRPC Exporter](https://docs.opennms.com/horizon/33/operation/deep-dive/grpc-exporter/grpc-exporter.html) and forwards alerts to the configured Altermanager instance(s).

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
| --headers             | Custom headers                                             |                |
| --key                 | TLS Key                                                    |                |
| --map.url             | Map Horizon instance ID's to URLs                          |                |
| --metrics.address     | Metrics listen address                                     |                |
| --metrics.path        | Metrics path                                               | /metrics       |
| --silent              | Disable all logging                                        |                |

All command line options may also be provided as environment variables with the prefix of `ONMS_GRPC` as follows:

```sh
export ONMS_GRPC_DEBUG="true"
export ONMS_GRPC_ALERTMANAGER_URL="http://am-0:9091,http://am-1:9091"
onms-grpc-receiver spog
```

## Alertmanager Integration

There is a basic implementation of sending data to an upstream Alertmanager instance/cluster.

This process sends a batch of alerts as they come in.

You may either specify via one or more `--alertmanager.url` as follows:

```sh
onms-grpc-receiver spog --alertmanager.url http://am-0:9091 --alertmanager.url http://am-1:9091 
```

Or you may use SRV recork lookups using the `--alertmanager.srv` and optionally `--alertmanager.scheme` as follows

```sh
onms-grpc-receiver spog --alertmanager.srv _http.alertmanager --alertmanager.scheme http
```

The above options are mutually exclusive, in addition only basic validation of provided URLs is done, not that any Alertmanager is reachable on startup.

### Alert Names and Labels

The alert name sent to Alertmanager is the OpenNMS "uei" value such as "uei.opennms.org/nodes/nodeDown" or "uei.opennms.org/nodes/dataCollectionFailed".

Labels are set as follows:

| OpenNMS Alarm Field                      | Alertmanager Label |                                 |
|------------------------------------------|--------------------|---------------------------------|
| Node ID                                  | node_id            |                                 |
| Node Name                                | node_name          |                                 |
| Instance ID (UUID of Horizon instance)   | instance_id        |                                 |
| Instance Name (name of Horizon instance) | instance_name      |                                 |
| UEI of Alarm                             | alertname          |                                 |
| Alarm ID                                 | alarm_id           |                                 |
| Severity                                 | severity           |                                 |
| Service (name)                           | service            | Only present on service outages |
| Interface (IP address)                   | ip_address         | Only present on service outages |
| Node Location                            | site               |                                 |
| Reduction Key                            | reduction_key      |                                 |
| Clear Key                                | clear_key          |                                 |

### Alarm link/URL

The direct linking of an alarm in Alertmanager to OpenNMS is handled by providing a mapping of the Horizon instance to a base URL as follows:

```sh
onms-grpc-receiver spog --map.url "uuid-of-horizon-instance=http://horizon:8980/opennms/"
```

Based on the above an alert from `uuid-of-horizon-instance` with an alert ID `25` would result in a URL of `http://horizon:8980/opennms/alarm/detail.htm?id=25`

## Metrics

Prometheus metrics are exposed on the `/metrics` path (by default) when the `--metrics.address` flag is provided.

Enabling metrics also enables a health check endpoint at `/-/healthy` that responds with `200 OK`.
