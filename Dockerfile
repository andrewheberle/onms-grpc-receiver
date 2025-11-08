FROM golang:1.24@sha256:c3ea4172c1dd39e1c90bb36a11ef95af6d0ccbb1d7cdedbb5dd14988c324d689 AS builder

COPY . /build

RUN cd /build && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' ./cmd/onms-grpc-receiver

FROM gcr.io/distroless/base-debian12:nonroot@sha256:0a0dc2036b7c56d1a9b6b3eed67a974b6d5410187b88cbd6f1ef305697210ee2

COPY --from=builder /build/onms-grpc-receiver /app/onms-grpc-receiver

ENV ONMS_GRPC_ADDRESS=":8080"

EXPOSE 8080

ENTRYPOINT [ "/app/onms-grpc-receiver" ]
