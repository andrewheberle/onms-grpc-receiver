FROM gcr.io/distroless/base-debian13:nonroot@sha256:e00da4d3bd422820880b080115b3bad24349bef37ed46d68ed0d13e150dc8d67
ARG TARGETPLATFORM
ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"
EXPOSE 8080
ENTRYPOINT [ "/usr/bin/onms-grpc-receiver", "spog" ]
COPY $TARGETPLATFORM/myprogram /usr/bin/

