.PHONY: lint format check audio-report audio-playback

lint:
	uv run ruff check .

format:
	uv run ruff format .

check: lint
	uv run ruff format --check .

# Pass extra script flags with ARGS, e.g.
# make audio-playback ARGS="--single-stream --idle-wait 300"
audio-report:
	uv run --no-dev python tests/manual_audio_check.py $(ARGS)

audio-playback:
	uv run --no-dev python tests/manual_audio_check.py --play $(ARGS)
