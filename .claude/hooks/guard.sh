#!/bin/sh
export GUARD_HOME=/tmp/guard-real
export GUARD_PROTO="$CLAUDE_PROJECT_DIR/proto/runtime/v1/runtime.proto"
exec node "$CLAUDE_PROJECT_DIR/adapters/claude-code/dist/index.js"
