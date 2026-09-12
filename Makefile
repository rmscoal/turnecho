.PHONY: lint format check e2e

lint:
	uv run ruff check .

format:
	uv run ruff format .

check: lint
	uv run ruff format --check .

# E2E_QUIET=1 skips audio playback; by default all summaries play aloud.
e2e:
	uv run --no-dev python scripts/e2e_hook_check.py
