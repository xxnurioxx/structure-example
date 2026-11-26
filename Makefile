.PHONY: run build lint image

run:
	go run .

build:
	CGO_ENABLED=0 go build -trimpath -o bin/kdeploy .

lint:
	go vet ./... && test -z "$$(gofmt -l .)"

image:
	docker buildx build -t ghcr.io/acme/kdeploy:dev .
