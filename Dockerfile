# syntax=docker/dockerfile:1.7
# ---------- 1. build the web UI ----------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 2. build the Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pocket-documents ./cmd/pocket-documents

# ---------- 3. runtime ----------
# OCRmyPDF + Tesseract make the PDF/A twin with its text layer, Poppler reads
# text and renders thumbnails/pages, Ghostscript and qpdf are OCRmyPDF's
# helpers, unpaper cleans scans. OCR_LANGS picks the Tesseract language packs
# baked into the image (Alpine package names without the prefix).
FROM alpine:3.21
ARG OCR_LANGS="eng deu fra ita spa nld por hrv srp srp_latn slv pol ces hun ron"
RUN apk add --no-cache ca-certificates tzdata curl \
      ocrmypdf tesseract-ocr tesseract-ocr-data-osd ghostscript qpdf poppler-utils unpaper pngquant jbig2enc py3-pillow \
 && for l in $OCR_LANGS; do apk add --no-cache tesseract-ocr-data-$l || echo "no language pack $l"; done \
 && addgroup -g 1000 app && adduser -u 1000 -G app -D -h /data app
COPY --from=build /out/pocket-documents /usr/local/bin/pocket-documents
ENV DATA_DIR=/data PORT=8080 HOST=0.0.0.0 OCR_LANGUAGE=eng
VOLUME ["/data"]
EXPOSE 8080
USER app
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD curl -fsS http://localhost:8080/healthz || exit 1
LABEL org.opencontainers.image.title="Pocket Documents" \
      org.opencontainers.image.description="Self-hosted document archive: OCR into searchable PDF/A, full-text search, tags, rules and a classifier that learns how you file. A Paperless-ngx replacement in one binary." \
      org.opencontainers.image.source="https://github.com/abjelosevic88/pocket-homelab-documents" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["pocket-documents"]
