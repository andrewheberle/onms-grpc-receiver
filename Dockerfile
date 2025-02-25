FROM golang:1.23@sha256:e5aad6ed4d73261f7d4a8740a734f12ecc5c5c7f6e61006a3cb54fa4c1081a7a AS builder

COPY . /build

RUN cd /build && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' ./cmd/onms-grpc-receiver

FROM gcr.io/distroless/base-debian12:nonroot@sha256:97d15218016debb9b6700a8c1c26893d3291a469852ace8d8f7d15b2f156920f

COPY --from=builder /build/onms-grpc-receiver /app/onms-grpc-receiver

ENV ONMS_GRPC_ADDRESS=":8080"

EXPOSE 8080

ENTRYPOINT [ "/app/onms-grpc-receiver" ]
