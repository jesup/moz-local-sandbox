.PHONY: all build test clean

# Default: build the netproxy binary into bin/.
all: build

build: bin/ccode-netproxy

bin/ccode-netproxy: netproxy/go.mod netproxy/*.go
	@mkdir -p bin
	cd netproxy && go build -trimpath -o ../bin/ccode-netproxy .

test:
	cd netproxy && go test ./...
	./test/test-macos.sh

clean:
	rm -rf bin
