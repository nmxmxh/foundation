#!/bin/zsh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
PROTO_DIR="$ROOT_DIR/foundation/runtime-transport/protos"
GO_OUT_DIR="$ROOT_DIR/foundation/runtime-transport/go/generated"
TS_OUT_DIR="$ROOT_DIR/foundation/runtime-transport/ts/src/generated"

mkdir -p "$GO_OUT_DIR"
mkdir -p "$TS_OUT_DIR"

protoc \
  -I "$PROTO_DIR" \
  --go_out=paths=source_relative:"$GO_OUT_DIR" \
  "$PROTO_DIR/transport/v1/metadata.proto" \
  "$PROTO_DIR/transport/v1/envelope.proto"

TS_PROTO_PLUGIN="${TS_PROTO_PLUGIN:-}"
if [[ -z "$TS_PROTO_PLUGIN" ]]; then
  if [[ -x "$ROOT_DIR/frontend/node_modules/.bin/protoc-gen-ts_proto" ]]; then
    TS_PROTO_PLUGIN="$ROOT_DIR/frontend/node_modules/.bin/protoc-gen-ts_proto"
  else
    echo "ts-proto plugin not found" >&2
    exit 1
  fi
fi

find "$TS_OUT_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} +

protoc \
  -I "$PROTO_DIR" \
  --plugin=protoc-gen-ts_proto="$TS_PROTO_PLUGIN" \
  --ts_proto_out="$TS_OUT_DIR" \
  --ts_proto_opt=esModuleInterop=true,useOptionals=messages,outputEncodeMethods=true,outputJsonMethods=false,outputClientImpl=false,env=browser \
  "$PROTO_DIR/transport/v1/metadata.proto" \
  "$PROTO_DIR/transport/v1/envelope.proto"

echo "generated runtime transport protobuf bindings"
