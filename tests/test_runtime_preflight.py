import io
import unittest
from contextlib import redirect_stderr
from unittest.mock import Mock, call, patch

from turnecho import runtime_preflight


class RuntimePreflightTests(unittest.TestCase):
    def test_imports_all_required_audio_modules(self) -> None:
        kittentts = Mock()
        sounddevice = Mock()

        with patch.object(
            runtime_preflight.importlib,
            "import_module",
            side_effect=[kittentts, sounddevice],
        ) as import_module:
            runtime_preflight.validate_runtime_dependencies()

        self.assertEqual(
            import_module.call_args_list,
            [call("kittentts"), call("sounddevice")],
        )
        sounddevice.check_output_settings.assert_called_once_with(samplerate=24000)
        kittentts.KittenTTS.assert_called_once_with("KittenML/kitten-tts-mini-0.8")

    def test_main_fails_when_a_runtime_module_cannot_load(self) -> None:
        with (
            patch.object(
                runtime_preflight,
                "validate_runtime_dependencies",
                side_effect=ImportError("sounddevice unavailable"),
            ),
            redirect_stderr(io.StringIO()),
        ):
            result = runtime_preflight.main()

        self.assertEqual(result, 1)


if __name__ == "__main__":
    unittest.main()
