FROM cgr.dev/chainguard/static:latest@sha256:c12f0107f8afb0bbc1b0eff96ff8675e85a10c6083d039b777c51fef5c27420f
ARG TARGETPLATFORM
ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"
EXPOSE 8080
ENTRYPOINT [ "/usr/bin/onms-grpc-receiver", "spog" ]
COPY $TARGETPLATFORM/onms-grpc-receiver /usr/bin/
USER 65532
