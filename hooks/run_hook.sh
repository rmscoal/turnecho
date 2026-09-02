#!/bin/sh

# Safely launches a TurnEcho hook with the installed runtime.
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

runtime="$HOME/.local/share/turnecho/runtimes/0.2.4/.venv"
if [ ! -x "$runtime/bin/python" ]; then
    empty_output
fi

if output=$("$runtime/bin/python" -m "$module"); then
    printf '%s\n' "$output"
else
    empty_output
fi
