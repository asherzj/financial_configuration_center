#!/usr/bin/env bash
set -euo pipefail

readonly FINCONFIG_REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
readonly FINCONFIG_TOOL_ROOT="${FINCONFIG_TOOL_ROOT:-${FINCONFIG_REPO_ROOT}/.cache/tools}"
readonly FINCONFIG_GO_CACHE="${FINCONFIG_GO_CACHE:-${FINCONFIG_REPO_ROOT}/.cache/go-build}"
readonly FINCONFIG_GO_MOD_CACHE="${FINCONFIG_GO_MOD_CACHE:-${FINCONFIG_REPO_ROOT}/.cache/go-mod}"
readonly BUF="${FINCONFIG_TOOL_ROOT}/buf-1.58.0/buf/bin/buf"
readonly PROTOC="${FINCONFIG_TOOL_ROOT}/protoc-35.0/bin/protoc"
readonly MODULE="github.com/asherzj/financial_configuration_center"

cd "${FINCONFIG_REPO_ROOT}"
export FINCONFIG_TOOL_ROOT
"${FINCONFIG_REPO_ROOT}/tools/bootstrap-tools.sh" >/dev/null

export BUF_CACHE_DIR="${FINCONFIG_REPO_ROOT}/.cache/buf"
export GOCACHE="${FINCONFIG_GO_CACHE}"
export GOMODCACHE="${FINCONFIG_GO_MOD_CACHE}"

"${BUF}" format -w
"${BUF}" lint
"${BUF}" generate

KITEX_TOOL_USE_PROTOC=1 go tool kitex \
  -module "${MODULE}" \
  -type protobuf \
  -I api/proto \
  -compiler-path "${PROTOC}" \
  -protobuf "Mfinconfig/config/v1/config.proto=${MODULE}/kitex_gen/finconfig/config/v1" \
  -protobuf "Mfinconfig/common/v1/common.proto=${MODULE}/kitex_gen/finconfig/common/v1" \
  api/proto/finconfig/config/v1/config.proto

KITEX_TOOL_USE_PROTOC=1 go tool kitex \
  -module "${MODULE}" \
  -type protobuf \
  -I api/proto \
  -compiler-path "${PROTOC}" \
  -protobuf "Mfinconfig/control/v1/control.proto=${MODULE}/kitex_gen/finconfig/control/v1" \
  -protobuf "Mfinconfig/config/v1/config.proto=${MODULE}/kitex_gen/finconfig/config/v1" \
  -protobuf "Mfinconfig/common/v1/common.proto=${MODULE}/kitex_gen/finconfig/common/v1" \
  api/proto/finconfig/control/v1/control.proto

gofmt -w gen/go kitex_gen
