GOENV = GOWORK=off GOFLAGS='-trimpath -p=2' GOMAXPROCS=3
GO = env $(GOENV) nice -n 15 go
.PHONY: build test gate check
build:
	$(GO) build -o o-lane .
test:
	$(GO) test ./...
gate:
	@test -z "$$(gofmt -l .)"
	$(GO) vet ./...
	$(GO) test -race ./...
check: gate
