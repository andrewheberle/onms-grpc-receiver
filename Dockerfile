FROM golang:1.25@sha256:01fa97d992fc0b54e908c80cba57f10bd5bec327d76adab1b8eb5c531d8318ab AS builder

COPY . /build

RUN cd /build && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' ./cmd/onms-grpc-receiver

FROM gcr.io/distroless/base-debian13:nonroot@sha256:e2f22688c7f48cf0657f7c0929b52174c80b73ea24ea898df7517c26621659bb

COPY --from=builder /build/onms-grpc-receiver /app/onms-grpc-receiver

ENV ONMS_GRPC_ADDRESS=":8080" ONMS_GRPC_METRICS_ADDRESS=":8081"

EXPOSE 8080

ENTRYPOINT [ "/app/onms-grpc-receiver", "spog" ]
