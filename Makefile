.PHONY: proto test build tidy webui webui-test webui-e2e

proto:
	protoc --go_out=. --go_opt=paths=source_relative proto/peer/v1/peer.proto

test:
	go test ./...

webui:
	cd webui && npm ci && npx nuxt generate
	mkdir -p internal/bridge/dist
	find internal/bridge/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r webui/.output/public/. internal/bridge/dist/

webui-test:
	cd webui && npx vitest run

webui-e2e:
	cd webui && npx playwright test

build: webui
	go build -o bin/node ./cmd/node
	go build -o bin/signal-server ./cmd/signal-server

tidy:
	go mod tidy
