FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/client ./cmd/client \
 && CGO_ENABLED=0 go build -trimpath -o /out/backend ./cmd/backend

FROM alpine:3.22
COPY --from=build /out/client /usr/local/bin/client
COPY --from=build /out/backend /usr/local/bin/backend
