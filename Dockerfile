## Builder
FROM golang:1.18-alpine AS builder

#RUN apk add --no-cache make

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
#RUN make build
RUN go build -v -o caddy-mesh-controller ./cmd/controller


## Image
FROM alpine:3.15

RUN addgroup -g 1000 -S app && \
    adduser -u 1000 -S app -G app

COPY --from=builder /usr/src/app/caddy-mesh-controller /app/

ENTRYPOINT ["/app/caddy-mesh-controller"]
