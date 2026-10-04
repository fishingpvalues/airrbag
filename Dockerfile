# syntax=docker/dockerfile:1

# 1. Browser script (TypeScript -> one minified IIFE).
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN mkdir -p ../internal/webassets/dist && npm run typecheck && npm run build

# 2. Static Go binary, cross-compiled for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webassets/dist/airrbag.js internal/webassets/dist/airrbag.js
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/airrbag ./cmd/airrbag

# 3. Runtime: no shell, no package manager, non-root, read-only friendly.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="airrbag" \
      org.opencontainers.image.description="Reverse proxy for the *Arr apps that shows where every file came from and blocks deletes that would break a private-tracker seed" \
      org.opencontainers.image.source="https://github.com/fishingpvalues/airrbag" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/airrbag /airrbag
USER nonroot:nonroot
ENV AIRRBAG_CONFIG=/config/airrbag.yml
HEALTHCHECK --interval=30s --timeout=6s --start-period=60s CMD ["/airrbag", "healthcheck"]
ENTRYPOINT ["/airrbag"]
CMD ["serve"]
