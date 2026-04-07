FROM golang:1.26.1-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/cryptobot ./cmd/cryptobot

FROM alpine:3.22

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /out/cryptobot /usr/local/bin/cryptobot

ENV TZ=Asia/Tokyo
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/cryptobot"]
