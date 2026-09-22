FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY services/backend/go.mod services/backend/go.sum ./
RUN go mod download
COPY services/backend/cmd ./cmd
COPY services/backend/internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /scanner ./cmd/scanner
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates clamav clamav-freshclam && rm -rf /var/lib/apt/lists/*
COPY --from=build /scanner /scanner
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/scanner"]
