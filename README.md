# OpenNMS gRPC Receiver

This is an experiment that acts as a receiver for the [OpenNMS gRPC Exporter](https://docs.opennms.com/horizon/33/operation/deep-dive/grpc-exporter/grpc-exporter.html).

## Alertmanager Integration

There is a basic implementation of sending data to an upstream Alertmanager instance.

This process does not look up SRV records to work with an AM cluster and has no retry login in place, it simple sends a batch of alerts as they come in.
