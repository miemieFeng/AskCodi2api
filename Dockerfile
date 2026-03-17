FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o askcodi-go .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/askcodi-go .
COPY --from=builder /build/static ./static

EXPOSE 8000

ENV LISTEN_ADDR=:8000
ENV DATABASE_PATH=/app/data/data.db

VOLUME ["/app/data"]

ENTRYPOINT ["./askcodi-go"]
