FROM gcr.io/distroless/base-debian13:nonroot@sha256:0896741ba5bafd3ac87ea025a5f578952f2d238ddc3614cb368acc983a687aa2
ARG TARGETPLATFORM
ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"
EXPOSE 8080
ENTRYPOINT [ "/usr/bin/onms-grpc-receiver", "spog" ]
COPY $TARGETPLATFORM/onms-grpc-receiver /usr/bin/
