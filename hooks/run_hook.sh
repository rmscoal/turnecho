#!/bin/sh

# Safely launches a TurnEcho hook with the installed runtime.
# Missing or invalid runtime inputs produce empty JSON for the calling host.

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

# Codex provides its plugin directory as PLUGIN_ROOT; Claude Code provides
# CLAUDE_PLUGIN_ROOT instead. Neither name is ours to change, so resolve both
# explicitly into one local plugin root.
codex_plugin_root="${PLUGIN_ROOT:-}"
claude_plugin_root="${CLAUDE_PLUGIN_ROOT:-}"
plugin_root="${claude_plugin_root:-$codex_plugin_root}"
if [ -z "$plugin_root" ] || [ ! -f "$plugin_root/pyproject.toml" ]; then
    empty_output
fi

runtime="$HOME/.local/share/turnecho/runtimes/0.3.0/.venv"
if [ ! -x "$runtime/bin/python" ]; then
    empty_output
fi

if output=$("$runtime/bin/python" -m "$module"); then
    printf '%s\n' "$output"
else
    empty_output
fi
