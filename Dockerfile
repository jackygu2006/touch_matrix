# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o apkclaw-server .

# Run stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder /app/apkclaw-server .
COPY static/ ./static/

EXPOSE 8443

ENV ADMIN_KEY=admin123
ENV DB_PATH=/app/data/apkclaw.db
ENV LISTEN_ADDR=:8443

RUN mkdir -p /app/data

CMD ["./apkclaw-server"]
