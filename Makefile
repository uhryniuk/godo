BINARY_NAME=godo
CMD_DIR=.
# CMD_DIR=cmd/$(BINARY_NAME)

.PHONY: build run test clean

build:
	go build -o $(BINARY_NAME) ./$(CMD_DIR)

godo: build
	./$(BINARY_NAME)

test:
	go test ./...

clean:
	rm -f $(BINARY_NAME)

