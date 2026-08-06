VERSION ?= 0.8.8

.PHONY: build release test vet fmt

build:
	go build -buildvcs=false -o bin/titop ./cmd/titop

release:
	mkdir -p release
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o release/titop-linux-arm64 ./cmd/titop
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o release/titop-linux-amd64 ./cmd/titop
	cp release/titop-linux-arm64 release/titop

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal
