# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/payment-simulator \
    ./cmd/payment-simulator

FROM scratch

COPY --from=build --chown=65532:65532 /out/payment-simulator /payment-simulator

USER 65532:65532
WORKDIR /data
EXPOSE 18082
VOLUME ["/data"]

HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=5 \
    CMD ["/payment-simulator", "healthcheck"]

ENTRYPOINT ["/payment-simulator"]
