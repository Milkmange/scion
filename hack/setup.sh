#!/bin/bash
set -e

# hack/setup.sh - Setup isolated test environment

REPO_ROOT=$(pwd)
TEST_DIR="${REPO_ROOT}/../qa-scion"
BIN_DIR="${HOME}/UNIX/bin"

echo "=== Setting up test environment in ${TEST_DIR} ==="

mkdir -p "${TEST_DIR}"
mkdir -p "${BIN_DIR}"

echo "=== Building scion binary to ${BIN_DIR} ==="
go build -o "${BIN_DIR}/scion" ./cmd/scion

cd "${TEST_DIR}"
if [ ! -d ".git" ]; then
    git init -q
fi
echo ".scion/agents/" > .gitignore

echo "=== Initializing grove ==="
scion grove init

echo "=== Setup Complete ==="
ls -A1 .scion