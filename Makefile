.PHONY: help build test test-race cover vet fmt fmt-check check smoke clean

SMOKE_DIR := .tmp/smoke
SMOKE_HOME := $(SMOKE_DIR)/home
SMOKE_BIN := $(SMOKE_DIR)/turnecho

help: ## Show available targets.
	@echo "TurnEcho targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'

build: ## Compile every package.
	@echo "== build =="
	@go build ./...

test: ## Run unit tests.
	@echo "== test =="
	@go test ./...

test-race: ## Run unit tests with the race detector.
	@echo "== test -race =="
	@go test -race ./...

cover: ## Run unit tests with coverage.
	@echo "== test -cover =="
	@go test -cover ./...

vet: ## Vet every package.
	@echo "== vet =="
	@go vet ./...

fmt: ## Format sources in place.
	@echo "== gofmt -w =="
	@gofmt -w .

fmt-check: ## Fail when any source is unformatted.
	@echo "== gofmt check =="
	@test -z "$$(gofmt -l .)"

check: vet fmt-check test ## Run the full gate: vet, format check, tests.

smoke: ## Scripted end-to-end check with a throwaway HOME under .tmp/.
	@echo "== smoke: building test binary =="
	@rm -rf $(SMOKE_DIR) && mkdir -p $(SMOKE_HOME)
	@go build -o $(SMOKE_BIN) ./cmd/turnecho
	@echo "== smoke: config defaults =="
	@HOME=$(SMOKE_HOME) $(SMOKE_BIN) config show
	@HOME=$(SMOKE_HOME) $(SMOKE_BIN) config show | grep -q "voice: speaker-0"
	@echo "OK defaults"
	@echo "== smoke: config set rejects bad values =="
	@code=0; HOME=$(SMOKE_HOME) $(SMOKE_BIN) config set speed fast >/dev/null 2>&1 || code=$$?; test $$code -eq 2
	@echo "OK bad speed exits 2"
	@HOME=$(SMOKE_HOME) $(SMOKE_BIN) config set voice speaker-3 | grep -q "voice: speaker-3"
	@echo "OK set voice"
	@echo "== smoke: list commands =="
	@HOME=$(SMOKE_HOME) $(SMOKE_BIN) voices --json | grep -q "speaker-0"
	@HOME=$(SMOKE_HOME) $(SMOKE_BIN) models --json | grep -q "kokoro-en-v0_19"
	@echo "OK voices and models"
	@echo "== smoke: say writes a valid wav =="
	@HOME=$(SMOKE_HOME) $(SMOKE_BIN) say hello --output $(SMOKE_DIR)/out.wav
	@test "$$(wc -c < $(SMOKE_DIR)/out.wav | tr -d ' ')" = "48044"
	@echo "OK wav bytes (44 header + 24000 samples)"
	@echo "== smoke: hook stop queues one job =="
	@test "$$(printf '%s' '{"hook_event_name":"Stop","session_id":"s1","turn_id":"t1","last_assistant_message":"Done.\n\n<!-- turnecho-summary:v1\nFixed the bug.\n-->\n"}' | HOME=$(SMOKE_HOME) $(SMOKE_BIN) hook stop)" = "{}"
	@echo "OK valid marker prints {}"
	@test "$$(printf '%s' '{"hook_event_name":"Stop","session_id":"s1","turn_id":"t2","last_assistant_message":"Plain reply."}' | HOME=$(SMOKE_HOME) $(SMOKE_BIN) hook stop)" = "{}"
	@echo "OK missing marker prints {}"
	@echo "== smoke: doctor =="
	@if command -v afplay >/dev/null 2>&1 || command -v aplay >/dev/null 2>&1; then HOME=$(SMOKE_HOME) $(SMOKE_BIN) doctor; else echo "SKIP doctor: no audio player in PATH"; fi
	@echo "== smoke: worker speaks the queued job =="
	@echo "(one second of silence is expected: Step 5 ships a stub backend)"
	@HOME=$(SMOKE_HOME) $(SMOKE_BIN) worker
	@echo "OK worker exit 0"
	@echo "== smoke passed =="

clean: ## Remove local scratch dirs, binaries, and the test cache.
	@echo "== clean =="
	@rm -rf .tmp turnecho
	@go clean -testcache
