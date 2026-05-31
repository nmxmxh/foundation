#!/bin/zsh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FOUNDATION_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOT_DIR="$(cd "$FOUNDATION_DIR/.." && pwd)"
PROTO_DIR="$FOUNDATION_DIR/runtime-transport/protos"
GO_OUT_DIR="$FOUNDATION_DIR/runtime-transport/go/generated"
TS_OUT_DIR="$FOUNDATION_DIR/runtime-transport/ts/src/generated"

mkdir -p "$GO_OUT_DIR"
mkdir -p "$TS_OUT_DIR"

PROTO_FILES=(
  "$PROTO_DIR/foundation/v1/metadata.proto"
  "$PROTO_DIR/foundation/v1/envelope.proto"
  "$PROTO_DIR/foundation/v1/projection.proto"
)

find "$GO_OUT_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} +

protoc \
  -I "$PROTO_DIR" \
  --go_out=paths=source_relative:"$GO_OUT_DIR" \
  "${PROTO_FILES[@]}"

resolve_ts_proto_plugin() {
  local configured="${TS_PROTO_PLUGIN:-}"
  if [[ -n "$configured" ]]; then
    if [[ -x "$configured" ]]; then
      echo "$configured"
      return
    fi
    echo "TS_PROTO_PLUGIN is set but not executable: $configured" >&2
    exit 1
  fi

  local candidate
  for candidate in \
    "$FOUNDATION_DIR/runtime-transport/ts/node_modules/.bin/protoc-gen-ts_proto" \
    "$ROOT_DIR/frontend/node_modules/.bin/protoc-gen-ts_proto"; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return
    fi
  done

  candidate="$(command -v protoc-gen-ts_proto 2>/dev/null || true)"
  if [[ -n "$candidate" && -x "$candidate" ]]; then
    echo "$candidate"
    return
  fi

  echo "ts-proto plugin not found" >&2
  echo "Install Foundation Core transport deps with: npm --prefix runtime-transport/ts install" >&2
  echo "Or provide an executable plugin with TS_PROTO_PLUGIN=/path/to/protoc-gen-ts_proto" >&2
  exit 1
}

TS_PROTO_PLUGIN="$(resolve_ts_proto_plugin)"

find "$TS_OUT_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} +

protoc \
  -I "$PROTO_DIR" \
  --plugin=protoc-gen-ts_proto="$TS_PROTO_PLUGIN" \
  --ts_proto_out="$TS_OUT_DIR" \
  --ts_proto_opt=esModuleInterop=true,useOptionals=messages,outputEncodeMethods=true,outputJsonMethods=false,outputClientImpl=false,env=browser \
  "${PROTO_FILES[@]}"

echo "generated runtime transport protobuf bindings"
