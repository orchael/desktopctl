BINARY := ai-desktops
CMD     := ./cmd/ai-desktops

.PHONY: build test clean deps

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

clean:
	rm -f $(BINARY)

deps:
	@which pulumi > /dev/null 2>&1 || (echo "Installing Pulumi..." && curl -fsSL https://get.pulumi.com | sh)
	@which aws > /dev/null 2>&1 || echo "WARNING: AWS CLI not found — install from https://aws.amazon.com/cli/"
	@which go > /dev/null 2>&1 || echo "WARNING: Go not found — install from https://go.dev/dl/"
