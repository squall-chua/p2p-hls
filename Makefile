.PHONY: proto test build tidy

proto:
	protoc --go_out=. --go_opt=paths=source_relative proto/peer/v1/peer.proto

test:
	go test ./...

build:
	go build -o bin/node ./cmd/node
	go build -o bin/signal-server ./cmd/signal-server

tidy:
	go mod tidy
