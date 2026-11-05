FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/syscon-bench ./cmd/syscon-bench

FROM alpine:3.22
RUN apk add --no-cache ca-certificates socat wget
COPY --from=build /out/syscon-bench /usr/local/bin/syscon-bench
COPY data/fields.csv /data/fields.csv
COPY deploy/docker/messages.txt /data/messages.txt
COPY log-generator/scenarios /scenarios
COPY deploy/docker/bench-entry.sh /bench-entry.sh
RUN chmod +x /bench-entry.sh
ENTRYPOINT ["/bench-entry.sh"]
