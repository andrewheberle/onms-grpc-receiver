FROM golang:1.25@sha256:dfae680962532eeea67ab297f1166c2c4e686edb9a8f05f9d02d96fc9191833e AS builder

COPY . /build

RUN cd /build && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' ./cmd/onms-grpc-receiver

FROM gcr.io/distroless/base-debian13:nonroot@sha256:e00da4d3bd422820880b080115b3bad24349bef37ed46d68ed0d13e150dc8d67

COPY --from=builder /build/onms-grpc-receiver /app/onms-grpc-receiver

ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"

EXPOSE 8080

ENTRYPOINT [ "/app/onms-grpc-receiver", "spog" ]
