FROM golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /prox ./cmd/prox

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk add --no-cache ca-certificates
COPY --from=build /prox /usr/local/bin/prox
RUN ln -s /usr/local/bin/prox /usr/bin/prox && ln -s /usr/local/bin/prox /bin/prox
CMD ["/usr/local/bin/prox", "serve", "-config", "/etc/prox/config.json5"]
