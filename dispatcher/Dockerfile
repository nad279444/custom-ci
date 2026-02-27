FROM golang:1.22-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o dispatcher ./dispatcher

FROM alpine:3.19
RUN apk add --no-cache ca-certificates sqlite
WORKDIR /app
COPY --from=builder /app/dispatcher .
RUN mkdir -p /data
VOLUME ["/data"]
EXPOSE 8080
CMD ["./dispatcher"]