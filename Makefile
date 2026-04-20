.PHONY: build install clean test test-e2e scratch-repo

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o build/ralph ./cmd/ralph

install: build
	@mkdir -p $(firstword $(GOPATH) $(HOME)/go)/bin
	cp build/ralph $(firstword $(GOPATH) $(HOME)/go)/bin/ralph
	./build/ralph install-skill

clean:
	rm -rf build/

test:
	go test ./...

# E2E test targets — run against a temp jj repo with trivial stories
# Usage: make test-e2e TEST=serial-single
#        make test-e2e TEST=parallel-independent
test-e2e: build
	@if [ -z "$(TEST)" ]; then echo "Usage: make test-e2e TEST=<test-name>"; echo "Tests: serial-single serial-basic parallel-independent parallel-deps plan quality judge idle"; exit 1; fi
	./testdata/run-test.sh $(TEST) $(RALPH_FLAGS)

# Throwaway repo for daemon/viewer verification. Empty userStories keeps the
# daemon idle (no API calls) when launched with --idle. Recreated on every
# invocation; /tmp/ralph-scratch is ours to clobber.
scratch-repo:
	@rm -rf /tmp/ralph-scratch
	@mkdir -p /tmp/ralph-scratch
	@cd /tmp/ralph-scratch && git init -q
	@printf '{"project":"scratch","branchName":"ralph/scratch","description":"verification fixture","userStories":[]}\n' > /tmp/ralph-scratch/prd.json
	@echo "Scratch repo ready at /tmp/ralph-scratch"
	@echo "Start daemon: ./build/ralph --dir /tmp/ralph-scratch --idle --daemon &"
	@echo "Stop daemon:  pkill -f 'ralph.*ralph-scratch'"
