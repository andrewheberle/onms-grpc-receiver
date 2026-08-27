FROM gcr.io/distroless/base-debian13:nonroot@sha256:d199d20fb09c898d8822ae5cbd5cf3c6d424e9b5e1fc2eb9a719a7752cd9d861
ARG TARGETPLATFORM
ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"
EXPOSE 8080
ENTRYPOINT [ "/usr/bin/onms-grpc-receiver", "spog" ]
COPY $TARGETPLATFORM/onms-grpc-receiver /usr/bin/
