CORE_MODULES := models album-queue dynamic-playlists spotdl-wapper
CORE_BIN_MODULES := album-queue dynamic-playlists spotdl-wapper

.PHONY: test-core build-core-linux lint-core scan-secrets verify-core

test-core:
	@set -e; \
	for module in $(CORE_MODULES); do \
		echo "==> go test $$module"; \
		(cd $$module && go test ./...); \
	done

build-core-linux:
	@set -e; \
	for module in $(CORE_BIN_MODULES); do \
		echo "==> go build linux/amd64 $$module"; \
		(cd $$module && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...); \
	done

lint-core:
	@command -v golangci-lint >/dev/null || (echo "golangci-lint is required" && exit 1)
	@set -e; \
	for module in $(CORE_MODULES); do \
		echo "==> golangci-lint $$module"; \
		(cd $$module && golangci-lint run ./...); \
	done

scan-secrets:
	@command -v gitleaks >/dev/null || (echo "gitleaks is required" && exit 1)
	@gitleaks detect --source . --no-git --redact

verify-core: test-core build-core-linux
