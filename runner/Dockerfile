# Builder stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o runner ./runner

# Runtime — keep Go installed so it can run go test
FROM golang:1.22-alpine
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY --from=builder /app/runner .
RUN mkdir -p /tmp/ci-runner
ENV WORK_DIR=/tmp/ci-runner
EXPOSE 8090
CMD ["./runner"]