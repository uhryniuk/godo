BINARY_NAME=godo
CMD_DIR=.

.PHONY: build run test clean

build:
	go build -o $(BINARY_NAME) ./$(CMD_DIR)

godo: build
	./$(BINARY_NAME)

test:
	go test ./...

clean:
	rm -f $(BINARY_NAME)

