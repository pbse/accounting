FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY workflows ./workflows
RUN CGO_ENABLED=0 go build -trimpath -o /api ./cmd/api && CGO_ENABLED=0 go build -trimpath -o /worker ./cmd/worker
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates poppler-utils tesseract-ocr tesseract-ocr-eng util-linux && rm -rf /var/lib/apt/lists/*
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /api /worker /
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/api"]
