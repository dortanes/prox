FROM golang:1.27.1-alpine3.24@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /prox ./cmd/prox

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
RUN apk add --no-cache ca-certificates
COPY --from=build /prox /usr/local/bin/prox
RUN ln -s /usr/local/bin/prox /usr/bin/prox && ln -s /usr/local/bin/prox /bin/prox
CMD ["/usr/local/bin/prox", "serve", "-config", "/etc/prox/config.json5"]
