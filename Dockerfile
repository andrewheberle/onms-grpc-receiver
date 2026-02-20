FROM golang:1.24@sha256:d2d2bc1c84f7e60d7d2438a3836ae7d0c847f4888464e7ec9ba3a1339a1ee804 AS builder

COPY . /build

RUN cd /build && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' ./cmd/onms-grpc-receiver

FROM gcr.io/distroless/base-debian12:nonroot@sha256:8b9f2e503e55aff85b79d6b22c7a63a65170e8698ae80de680e3f5ea600977bf

COPY --from=builder /build/onms-grpc-receiver /app/onms-grpc-receiver

ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"

EXPOSE 8080

ENTRYPOINT [ "/app/onms-grpc-receiver", "spog" ]
