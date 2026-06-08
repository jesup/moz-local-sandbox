.PHONY: test clean

test:
	./test/test-macos.sh

clean:
	rm -rf bin
