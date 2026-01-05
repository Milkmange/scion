#!/bin/bash
set -e

# Build the binary
echo "Building scion..."
go build -buildvcs=false -o scion ./cmd/scion

# Check if binary exists
if [ ! -f ./scion ]; then
    echo "Build failed, ./scion not found"
    exit 1
fi

# Run help
echo "Running scion --help..."
./scion --help

# Run version (if available, assuming 'version' or similar command exists, 
# but help is guaranteed by Cobra usually)
# ./scion version

echo "Smoke test passed!"
rm ./scion
