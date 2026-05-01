.PHONY: build run test clean

build:
	go build -o bin/jarv ./cmd/jarv

run:
	./bin/jarv start

chat:
	./bin/jarv chat

oracle:
	./bin/jarv oracle "$(SCENARIO)"

mcp:
	./bin/jarv mcp serve

test:
	go test ./...

clean:
	rm -rf bin/
