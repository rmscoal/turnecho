#!/bin/sh

# Safely launches a TurnEcho hook through uv with the installed runtime.
# Missing or invalid runtime inputs produce empty JSON for Codex.

empty_output() {
    printf '{}\n'
    exit 0
}

case "${1:-}" in
    prompt)
        module="turnecho.prompt_hook"
        ;;
    stop)
        module="turnecho.stop_hook"
        ;;
    *)
        empty_output
        ;;
esac

plugin_root="${PLUGIN_ROOT:-}"
if [ -z "$plugin_root" ] || [ ! -f "$plugin_root/pyproject.toml" ]; then
    empty_output
fi

runtime="$HOME/.local/share/turnecho/runtimes/0.2.2/.venv"
if [ ! -x "$runtime/bin/python" ]; then
    runtime="$plugin_root/.venv"
fi
if [ ! -x "$runtime/bin/python" ] || ! command -v uv >/dev/null 2>&1; then
    empty_output
fi

if output=$(
    UV_PROJECT_ENVIRONMENT="$runtime" \
        uv run \
        --preview-features project-directory-must-exist \
        --project "$plugin_root" \
        --no-dev \
        --no-sync \
        python -m "$module"
); then
    printf '%s\n' "$output"
else
    empty_output
fi
