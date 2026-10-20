FROM gcr.io/distroless/base-debian13:nonroot@sha256:7e22204ffcba783587afba539fcc199cc8c91a7583b66299ac4dccdb5fed5f03
ARG TARGETPLATFORM
ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"
EXPOSE 8080
ENTRYPOINT [ "/usr/bin/onms-grpc-receiver", "spog" ]
COPY $TARGETPLATFORM/onms-grpc-receiver /usr/bin/
