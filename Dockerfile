FROM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /prox ./cmd/prox

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache ca-certificates
COPY --from=build /prox /usr/local/bin/prox
RUN ln -s /usr/local/bin/prox /usr/bin/prox && ln -s /usr/local/bin/prox /bin/prox
CMD ["/usr/local/bin/prox", "serve", "-config", "/etc/prox/config.json5"]
