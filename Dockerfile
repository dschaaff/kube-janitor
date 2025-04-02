# Build stage
FROM public.ecr.aws/docker/library/golang:1.24-alpine3.21 AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETPLATFORM

RUN export GOOS=$(echo $TARGETPLATFORM | cut -d/ -f1) \
  && export GOARCH=$(echo $TARGETPLATFORM | cut -d/ -f2) \
  && echo "Building for $GOOS/$GOARCH" \
  && CGO_ENABLED=0 go build -ldflags="-X main.version=${VERSION}" -o kube-janitor ./cmd/kube-janitor
# Final stage
FROM public.ecr.aws/docker/library/golang:1.24-alpine3.21

WORKDIR /

# Copy the binary from builder
COPY --from=builder /app/kube-janitor /kube-janitor

# Run as non-root user
USER 65534:65534

ENTRYPOINT ["/kube-janitor"]
