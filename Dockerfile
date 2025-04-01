# Build stage
FROM public.ecr.aws/docker/library/golang:1.24-alpine3.21 AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-X main.version=${VERSION}" -o kube-janitor ./cmd/kube-janitor

# Final stage
FROM public.ecr.aws/docker/library/golang:1.24-alpine3.21

WORKDIR /

# Copy the binary from builder
COPY --from=builder /app/kube-janitor /kube-janitor

# Run as non-root user
USER 65534:65534

ENTRYPOINT ["/kube-janitor"]
