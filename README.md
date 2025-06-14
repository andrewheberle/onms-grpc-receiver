# OpenNMS gRPC Receiver

This is an experiment that acts as a receiver for the [OpenNMS gRPC Exporter](https://docs.opennms.com/horizon/33/operation/deep-dive/grpc-exporter/grpc-exporter.html).

## Command Line Options

| Flag                  | Decription                                                 | Default        |
|-----------------------|------------------------------------------------------------|----------------|
| --address             | Service listen address                                     | localhost:8080 |
| --alertmanager.scheme | Alertmanager scheme (http/https) when SRV records are used | http           |
| --alertmanager.srv    | Alertmanager SRV Record                                    |                |
| --alertmanager.url    | Alertmanager URL                                           |                |
| --cert                | TLS Certificate                                            |                |
| --debug               | Enable debug logging                                       |                |
| --headers             | Custom headers                                             |                |
| --key                 | TLS Key                                                    |                |
| --map.url             | Map Horizon instance ID's to URLs                          |                |
| --silent              | Disable all logging                                        |                |

## Alertmanager Integration

There is a basic implementation of sending data to an upstream Alertmanager instance.

This process sends a batch of alerts as they come in.

### Alert Names and Labels

The alert name sent to Alertmanager is the OpenNMS "uei" value such as "uei.opennms.org/nodes/nodeDown" or "uei.opennms.org/nodes/dataCollectionFailed".

Labels are set as follows:

| OpenNMS Alarm Field                      | Alertmanager Label |                                 |
|------------------------------------------|--------------------|---------------------------------|
| Node ID                                  | node_id            |                                 |
| Node Name                                | node_name          |                                 |
| Instance ID (UUID of Horizon instance)   | instance_id        |                                 |
| Instance Name (name of Horizon instance) | instance_name      |                                 |
| Severity                                 | severity           |                                 |
| Service (name)                           | service            | Only present on service outages |
| Interface (IP address)                   | ip_address         | Only present on service outages |
| Node Location                            | site               |                                 |

### Alarm link/URL

The direct linking of an alarm in Alertmanager to OpenNMS is handled by providing a mapping of the Horizon instance to a base URL as follows:

```sh
onms-grpc-receiver spog --map.url "uuid-of-horizon-instance=http://horizon:8980/opennms/"
```

Based on the above an alert from `uuid-of-horizon-instance` with an alert ID `25` would result in a URL of `http://horizon:8980/opennms/alarm/detail.htm?id=25`


