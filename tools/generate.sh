#!/usr/bin/env bash
set -euo pipefail

readonly FINCONFIG_REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
readonly FINCONFIG_CONTRACTS_ROOT="${FINCONFIG_REPO_ROOT}/contracts"
readonly FINCONFIG_TOOL_ROOT="${FINCONFIG_TOOL_ROOT:-${FINCONFIG_REPO_ROOT}/.cache/tools}"
readonly FINCONFIG_GO_CACHE="${FINCONFIG_GO_CACHE:-${FINCONFIG_REPO_ROOT}/.cache/go-build}"
readonly FINCONFIG_GO_MOD_CACHE="${FINCONFIG_GO_MOD_CACHE:-${FINCONFIG_REPO_ROOT}/.cache/go-mod}"
readonly FINCONFIG_GO_TOOL_BIN="${FINCONFIG_TOOL_ROOT}/go-bin"
readonly BUF="${FINCONFIG_TOOL_ROOT}/buf-1.58.0/buf/bin/buf"
readonly PROTOC="${FINCONFIG_TOOL_ROOT}/protoc-35.0/bin/protoc"
readonly CONTRACTS_MODULE="github.com/asherzj/financial_configuration_center/contracts"
readonly COMPATIBILITY_MODULE="github.com/asherzj/financial_configuration_center"

export FINCONFIG_TOOL_ROOT
"${FINCONFIG_REPO_ROOT}/tools/bootstrap-tools.sh" >/dev/null

export BUF_CACHE_DIR="${FINCONFIG_REPO_ROOT}/.cache/buf"
export GOCACHE="${FINCONFIG_GO_CACHE}"
export GOMODCACHE="${FINCONFIG_GO_MOD_CACHE}"
export GOWORK=off
mkdir -p "${FINCONFIG_GO_TOOL_BIN}"
go -C "${FINCONFIG_REPO_ROOT}/tools" build -o "${FINCONFIG_GO_TOOL_BIN}/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go
go -C "${FINCONFIG_REPO_ROOT}/tools" build -o "${FINCONFIG_GO_TOOL_BIN}/protoc-gen-go-grpc" google.golang.org/grpc/cmd/protoc-gen-go-grpc
go -C "${FINCONFIG_REPO_ROOT}/tools" build -o "${FINCONFIG_GO_TOOL_BIN}/kitex" github.com/cloudwego/kitex/tool/cmd/kitex
export PATH="${FINCONFIG_GO_TOOL_BIN}:${PATH}"

cd "${FINCONFIG_CONTRACTS_ROOT}"
"${BUF}" format -w
"${BUF}" lint
"${BUF}" generate

KITEX_TOOL_USE_PROTOC=1 "${FINCONFIG_GO_TOOL_BIN}/kitex" \
  -module "${CONTRACTS_MODULE}" \
  -type protobuf \
  -gen-path kitex_gen \
  -I proto \
  -compiler-path "${PROTOC}" \
  -protobuf "Mfinconfig/config/v1/config.proto=${CONTRACTS_MODULE}/kitex_gen/finconfig/config/v1" \
  -protobuf "Mfinconfig/common/v1/common.proto=${CONTRACTS_MODULE}/kitex_gen/finconfig/common/v1" \
  proto/finconfig/config/v1/config.proto

KITEX_TOOL_USE_PROTOC=1 "${FINCONFIG_GO_TOOL_BIN}/kitex" \
  -module "${CONTRACTS_MODULE}" \
  -type protobuf \
  -gen-path kitex_gen \
  -I proto \
  -compiler-path "${PROTOC}" \
  -protobuf "Mfinconfig/control/v1/control.proto=${CONTRACTS_MODULE}/kitex_gen/finconfig/control/v1" \
  -protobuf "Mfinconfig/config/v1/config.proto=${CONTRACTS_MODULE}/kitex_gen/finconfig/config/v1" \
  -protobuf "Mfinconfig/common/v1/common.proto=${CONTRACTS_MODULE}/kitex_gen/finconfig/common/v1" \
  proto/finconfig/control/v1/control.proto

gofmt -w "${FINCONFIG_CONTRACTS_ROOT}/gen/go" "${FINCONFIG_CONTRACTS_ROOT}/kitex_gen"

# Expand/contract compatibility: root consumers still import the old bindings
# until the next migration commit switches them to a tagged Contracts release.
# Keep those bindings generated from their parity-guarded source copy meanwhile.
cd "${FINCONFIG_REPO_ROOT}"
"${BUF}" format -w
"${BUF}" lint
"${BUF}" generate

KITEX_TOOL_USE_PROTOC=1 "${FINCONFIG_GO_TOOL_BIN}/kitex" \
  -module "${COMPATIBILITY_MODULE}" \
  -type protobuf \
  -I api/proto \
  -compiler-path "${PROTOC}" \
  -protobuf "Mfinconfig/config/v1/config.proto=${COMPATIBILITY_MODULE}/kitex_gen/finconfig/config/v1" \
  -protobuf "Mfinconfig/common/v1/common.proto=${COMPATIBILITY_MODULE}/kitex_gen/finconfig/common/v1" \
  api/proto/finconfig/config/v1/config.proto

KITEX_TOOL_USE_PROTOC=1 "${FINCONFIG_GO_TOOL_BIN}/kitex" \
  -module "${COMPATIBILITY_MODULE}" \
  -type protobuf \
  -I api/proto \
  -compiler-path "${PROTOC}" \
  -protobuf "Mfinconfig/control/v1/control.proto=${COMPATIBILITY_MODULE}/kitex_gen/finconfig/control/v1" \
  -protobuf "Mfinconfig/config/v1/config.proto=${COMPATIBILITY_MODULE}/kitex_gen/finconfig/config/v1" \
  -protobuf "Mfinconfig/common/v1/common.proto=${COMPATIBILITY_MODULE}/kitex_gen/finconfig/common/v1" \
  api/proto/finconfig/control/v1/control.proto

gofmt -w "${FINCONFIG_REPO_ROOT}/gen/go" "${FINCONFIG_REPO_ROOT}/kitex_gen"
