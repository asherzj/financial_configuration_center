#!/usr/bin/env bash
set -euo pipefail

readonly BUF_VERSION="1.58.0"
readonly PROTOC_VERSION="35.0"
readonly FINCONFIG_TOOL_ROOT="${FINCONFIG_TOOL_ROOT:-$(pwd)/.cache/tools}"

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)
    readonly BUF_ARCHIVE="buf-Darwin-arm64.tar.gz"
    readonly BUF_SHA256="e2230c26fd9ef2f84c22ada5847ac673c04ad4b6c5e7f30ff972601f565b6c90"
    readonly PROTOC_ARCHIVE="protoc-35.0-osx-aarch_64.zip"
    readonly PROTOC_SHA256="45444963204757fd3e2fbe304bc1fdadfb488d8556ff099c4cc06575eab88976"
    ;;
  Linux-x86_64)
    readonly BUF_ARCHIVE="buf-Linux-x86_64.tar.gz"
    readonly BUF_SHA256="59f1426ff27aa1fb008f1ae4d494d9897f56844262ca414e84310d0b04b23e76"
    readonly PROTOC_ARCHIVE="protoc-35.0-linux-x86_64.zip"
    readonly PROTOC_SHA256="a45cda0989c17dd950db55f6fbe1e5814c50fda08e87aa422980ac1f89dddbbc"
    ;;
  Linux-aarch64)
    readonly BUF_ARCHIVE="buf-Linux-aarch64.tar.gz"
    readonly BUF_SHA256="ca6beb038838957db0fa7bc9ffc59ae4cfab10c1a52bbe1303e8ff746281854d"
    readonly PROTOC_ARCHIVE="protoc-35.0-linux-aarch_64.zip"
    readonly PROTOC_SHA256="36b518ac14d90351cc6598228ed2bbe5afe4e357b1af470b07e0ec1609875de2"
    ;;
  *)
    echo "unsupported tool platform: $(uname -s)-$(uname -m)" >&2
    exit 1
    ;;
esac

mkdir -p "${FINCONFIG_TOOL_ROOT}/downloads" "${FINCONFIG_TOOL_ROOT}/buf-${BUF_VERSION}" "${FINCONFIG_TOOL_ROOT}/protoc-${PROTOC_VERSION}"

download_and_verify() {
  local url="$1"
  local destination="$2"
  local expected="$3"
  if [[ ! -f "${destination}" ]]; then
    curl -fL --retry 3 --output "${destination}" "${url}"
  fi
  local actual
  actual="$(shasum -a 256 "${destination}" | awk '{print $1}')"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "checksum mismatch for ${destination}: expected ${expected}, got ${actual}" >&2
    exit 1
  fi
}

if [[ ! -x "${FINCONFIG_TOOL_ROOT}/buf-${BUF_VERSION}/buf/bin/buf" ]]; then
  download_and_verify \
    "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/${BUF_ARCHIVE}" \
    "${FINCONFIG_TOOL_ROOT}/downloads/${BUF_ARCHIVE}" \
    "${BUF_SHA256}"
  tar -xzf "${FINCONFIG_TOOL_ROOT}/downloads/${BUF_ARCHIVE}" -C "${FINCONFIG_TOOL_ROOT}/buf-${BUF_VERSION}"
fi

if [[ ! -x "${FINCONFIG_TOOL_ROOT}/protoc-${PROTOC_VERSION}/bin/protoc" ]]; then
  download_and_verify \
    "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/${PROTOC_ARCHIVE}" \
    "${FINCONFIG_TOOL_ROOT}/downloads/${PROTOC_ARCHIVE}" \
    "${PROTOC_SHA256}"
  unzip -q -o "${FINCONFIG_TOOL_ROOT}/downloads/${PROTOC_ARCHIVE}" -d "${FINCONFIG_TOOL_ROOT}/protoc-${PROTOC_VERSION}"
fi

echo "BUF=${FINCONFIG_TOOL_ROOT}/buf-${BUF_VERSION}/buf/bin/buf"
echo "PROTOC=${FINCONFIG_TOOL_ROOT}/protoc-${PROTOC_VERSION}/bin/protoc"
