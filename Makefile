.PHONY: test docker push

IMAGE            ?= dschaaff/kube-janitor
VERSION          ?= $(shell git describe --tags --always --dirty)
TAG              ?= $(VERSION)

default: docker

version:
	sed -i "s/version: v.*/version: v$(VERSION)/" deploy/*.yaml
	sed -i "s/kube-janitor:.*/kube-janitor:$(VERSION)/" deploy/*.yaml
	sed -i "s/appVersion:.*/appVersion: $(VERSION)/" unsupported/helm/Chart.yaml

docker:
	docker buildx create --use
	docker buildx build --rm --build-arg "VERSION=$(VERSION)" -t "$(IMAGE):$(TAG)" -t "$(IMAGE):latest" --platform linux/amd64,linux/arm64 .
	@echo 'Docker image $(IMAGE):$(TAG) multi-arch was build (cannot be used).'

push:
	docker buildx create --use
	docker buildx build --rm --build-arg "VERSION=$(VERSION)" -t "$(IMAGE):$(TAG)" -t "$(IMAGE):latest" --platform linux/amd64,linux/arm64 --push .
	@echo 'Docker image $(IMAGE):$(TAG) multi-arch can now be used.'

.PHONY: helm-docs
helm-docs:
	@helm-docs -o Values.md
.PHONY: all
all: test build

.PHONY: test
test:
	go test -v -race -cover ./...

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux go build -o kube-janitor ./cmd/kube-janitor

.PHONY: docker
docker:
	docker build -t dschaaff/kube-janitor:latest .

.PHONY: push-docker
push-docker: docker
	docker push dschaaff/kube-janitor:latest
VERSION ?= $(shell git describe --tags --always --dirty)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT ?= $(shell git rev-parse HEAD)

.PHONY: all
all: build test

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux go build \
		-ldflags="-X main.version=${VERSION} -X main.buildDate=${BUILD_DATE} -X main.gitCommit=${GIT_COMMIT}" \
		-o kube-janitor ./cmd/kube-janitor

.PHONY: test
test:
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...

.PHONY: docker
docker:
	docker build -t kube-janitor:${VERSION} \
		--build-arg VERSION=${VERSION} \
		.
