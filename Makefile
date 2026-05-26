.PHONY: run-bridge run-cli test fmt vet

# Run the Go bridge service
run-bridge:
	go run ./cmd/bridge

# Run the Python CLI (ensure venv is active or install deps)
run-cli:
	cd cli && python -m faulty_link_cli.main health

# Run all tests (Go + Python)
test: test-go test-py

test-go:
	go test ./...

test-py:
	cd cli && python -m pytest -q || true

# Go formatting and linting
fmt:
	go fmt ./...

vet:
	go vet ./...
