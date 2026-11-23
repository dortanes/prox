FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /prox ./cmd/prox

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk add --no-cache ca-certificates
COPY --from=build /prox /usr/local/bin/prox
RUN ln -s /usr/local/bin/prox /usr/bin/prox && ln -s /usr/local/bin/prox /bin/prox
CMD ["/usr/local/bin/prox", "serve", "-config", "/etc/prox/config.json5"]
