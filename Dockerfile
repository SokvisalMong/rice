# Build the bot as a static Go binary.
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache dependency downloads independently from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /rice .

# Keep the runtime image small and non-root.
FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S rice \
    && adduser -S -G rice rice \
    && mkdir /data \
    && chown rice:rice /data

WORKDIR /app
COPY --from=builder --chown=rice:rice /rice /app/rice

USER rice
VOLUME ["/data"]

ENTRYPOINT ["/app/rice"]
