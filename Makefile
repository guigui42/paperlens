.PHONY: test lint demo browser evaluate build
build:
	go build -o .cache/paperlens ./cmd/paperlens
test:
	go test -race ./...
lint:
	go vet ./...
	golangci-lint run
demo:
	PAPERLENS_DEMO=true go run ./cmd/paperlens
browser:
	npm run test:browser
evaluate:
	go run ./cmd/evaluate
