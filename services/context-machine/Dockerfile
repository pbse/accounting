FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY services/backend/go.mod services/backend/go.sum ./
RUN go mod download
COPY services/backend/cmd ./cmd
COPY services/backend/internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /context-machine ./cmd/context-machine

FROM node:24-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @openai/codex@0.157.1 @anthropic-ai/claude-code@2.1.284 \
    && codex --version && claude --version \
    && npm cache clean --force
COPY --from=build /context-machine /context-machine
USER node
EXPOSE 8080
ENTRYPOINT ["/context-machine"]
