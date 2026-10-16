.PHONY: all build install clean test

BINARY_NAME=dbq

all: build

build:
	go build -ldflags="-s -w" -o $(BINARY_NAME) .

install:
	go install .

test:
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
