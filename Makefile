OUTPUT ?= dist/mulint

default: build

build:
	go build -o $(OUTPUT) .

install:
	go install ./...

test:
	go test -race ./...

bin/golangci-lint:
	@test -x $$(go env GOPATH)/bin/golangci-lint || \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin

lint: bin/golangci-lint
	$$(go env GOPATH)/bin/golangci-lint run

fmt:
	go fmt ./...

clean:
	rm -rf ./dist

.PHONY: build install test lint fmt clean
