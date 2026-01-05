.PHONY: build clean

build:
	@echo "Building scion..."
	@go build -ldflags "$$(./hack/version.sh)" -o scion ./cmd/scion

clean:
	@rm -f scion
