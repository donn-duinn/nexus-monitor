FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /nexus-monitor .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates kubectl
COPY --from=builder /nexus-monitor /usr/local/bin/nexus-monitor
COPY config.yaml /etc/nexus-monitor/config.yaml
EXPOSE 8099
ENTRYPOINT ["nexus-monitor"]
CMD ["-config", "/etc/nexus-monitor/config.yaml"]
