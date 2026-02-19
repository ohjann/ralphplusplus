.PHONY: build clean

build:
	go build -o build/ralph ./cmd/ralph

clean:
	rm -rf build/
