# Reproducible build and test for AI Runtime Guard.
#
# Two independent stages — the Go core and the TypeScript adapters build and
# test without knowing about each other, which is the layering the architecture
# claims. The e2e stage is the only place they meet, and it meets them the way
# a real adapter does: over the socket.

# --- Go core ---------------------------------------------------------------
FROM golang:1.25-alpine AS core

WORKDIR /src/core

# Dependencies first so a source edit does not re-download the module graph.
COPY core/go.mod core/go.sum ./
RUN go mod download

COPY core/ ./
RUN go build ./... && go vet ./...
RUN go build -o /out/guard ./cmd/guard && go build -o /out/demo ./cmd/demo

# --- Go tests --------------------------------------------------------------
FROM core AS core-test
RUN go test ./... 2>&1

# --- TypeScript adapters ---------------------------------------------------
FROM node:22-alpine AS adapters

WORKDIR /src

RUN corepack enable

COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY adapters/shared/package.json ./adapters/shared/
COPY adapters/claude-code/package.json ./adapters/claude-code/
RUN pnpm install --frozen-lockfile

COPY proto/ ./proto/
COPY adapters/ ./adapters/
RUN pnpm -r build

# --- TypeScript tests ------------------------------------------------------
FROM adapters AS adapters-test
RUN pnpm -r test

# --- End to end ------------------------------------------------------------
# Starts the real daemon and drives it over the real socket. "The unit tests
# pass" and "the daemon decides correctly over the wire" are different claims,
# and only this stage checks the second one.
FROM node:22-alpine AS e2e

WORKDIR /app

COPY --from=core /out/guard /usr/local/bin/guard
COPY --from=adapters /src/adapters ./adapters
COPY --from=adapters /src/node_modules ./node_modules
COPY proto/ ./proto/
COPY scripts/e2e.sh /usr/local/bin/e2e

ENV GUARD_HOME=/app/state
ENV GUARD_PROTO=/app/proto/runtime/v1/runtime.proto

RUN chmod +x /usr/local/bin/e2e
CMD ["/usr/local/bin/e2e"]
