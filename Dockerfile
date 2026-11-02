FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
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
