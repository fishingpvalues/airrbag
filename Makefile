.PHONY: all web build test test-race lint fmt typecheck docker-build cover run clean tidy icons screenshots

APP := airrbag
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

all: web build

web/node_modules: web/package.json web/package-lock.json
	cd web && npm ci --no-audit --no-fund
	@touch web/node_modules

web: web/node_modules
	cd web && npm run build

web-test: web/node_modules
	cd web && npm test

# Regenerate the committed PNG icons from assets/logo.svg (needs rsvg-convert).
icons:
	scripts/icons.sh

typecheck: web/node_modules
	cd web && npm run typecheck

build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP) ./cmd/airrbag

test:
	go test ./... -count=1

test-race:
	go test -race ./... -count=1

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint: typecheck
	golangci-lint run

fmt:
	gofmt -s -w .

run: web build
	./$(APP) serve -config examples/airrbag.yml

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(APP):dev .

tidy:
	go mod tidy
	go mod verify

clean:
	rm -f $(APP) coverage.out coverage.html internal/webassets/dist/airrbag.js internal/webassets/dist/dashboard.js internal/webassets/dist/dashboard.css

screenshots:
	scripts/optimize-screenshots.sh
