.PHONY: build clean test-e2e

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o build/ralph ./cmd/ralph

clean:
	rm -rf build/

# E2E test targets — run against a temp jj repo with trivial stories
# Usage: make test-e2e TEST=serial-single
#        make test-e2e TEST=parallel-independent
test-e2e: build
	@if [ -z "$(TEST)" ]; then echo "Usage: make test-e2e TEST=<test-name>"; echo "Tests: serial-single serial-basic parallel-independent parallel-deps plan quality judge idle"; exit 1; fi
	./testdata/run-test.sh $(TEST) $(RALPH_FLAGS)
