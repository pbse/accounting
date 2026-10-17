FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY workflows ./workflows
COPY activities ./activities
RUN CGO_ENABLED=0 go build -trimpath -o /api ./cmd/api && CGO_ENABLED=0 go build -trimpath -o /worker ./cmd/worker
# release-test is the runtime's Debian release with the same document tools, plus Go: scripts/release-check.sh runs a
# revision's full test suite in it. Nothing below builds from it; the image deployed is the last stage.
FROM golang:1.27.1-bookworm AS release-test
COPY --from=node:24-bookworm-slim /usr/local/bin/node /usr/local/bin/node
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils tesseract-ocr tesseract-ocr-eng util-linux && rm -rf /var/lib/apt/lists/*
WORKDIR /repo/services/backend
CMD ["go", "test", "-count=1", "-p", "1", "./..."]

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates poppler-utils tesseract-ocr tesseract-ocr-eng util-linux && rm -rf /var/lib/apt/lists/*
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /api /worker /
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/api"]
