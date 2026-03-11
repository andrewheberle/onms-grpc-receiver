FROM golang:1.25@sha256:bd1e2df4e6259b2bd5b1de0e6b22ca414502cd6e7276a5dd5dd414b65063be58 AS builder

COPY . /build

RUN cd /build && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' ./cmd/onms-grpc-receiver

FROM gcr.io/distroless/base-debian13:nonroot@sha256:e00da4d3bd422820880b080115b3bad24349bef37ed46d68ed0d13e150dc8d67

COPY --from=builder /build/onms-grpc-receiver /app/onms-grpc-receiver

ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"

EXPOSE 8080

ENTRYPOINT [ "/app/onms-grpc-receiver", "spog" ]
