BINARY := devex-agent
CMD     := ./cmd/devex-agent

.PHONY: build test vet lint clean

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

test-verbose:
	go test -v ./...

test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY) coverage.out coverage.html

tidy:
	go mod tidy
