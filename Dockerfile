FROM cgr.dev/chainguard/static:latest@sha256:8658e9eaeb2294fa0e49b0667f7d66b4e21fa7690b8d2a4c30cd1d013ceb9eb7
ARG TARGETPLATFORM
ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"
EXPOSE 8080
ENTRYPOINT [ "/usr/bin/onms-grpc-receiver", "spog" ]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD [ "/usr/bin/onms-grpc-receiver", "healthcheck" ]
COPY $TARGETPLATFORM/onms-grpc-receiver /usr/bin/
USER 65532
