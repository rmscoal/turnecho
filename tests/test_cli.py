import io
import json
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import Mock, patch

from turnecho import cli, config


class CliTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = TemporaryDirectory()
        self.config_path = Path(self.temporary_directory.name) / "config.json"
        self.path_patch = patch.object(
            config,
            "TURNECHO_CONFIG_FILE_PATH",
            str(self.config_path),
        )
        self.path_patch.start()

    def tearDown(self) -> None:
        self.path_patch.stop()
        self.temporary_directory.cleanup()

    def run_cli(self, *arguments: str) -> tuple[int, str, str]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            result = cli.main(arguments)
        return result, stdout.getvalue(), stderr.getvalue()

    def test_show_uses_defaults_without_loading_audio(self) -> None:
        with patch.object(cli.importlib, "import_module") as import_module:
            result, stdout, stderr = self.run_cli("config", "show", "--json")

        self.assertEqual(result, 0)
        self.assertEqual(json.loads(stdout)["model"], "mini")
        self.assertEqual(json.loads(stdout)["voice"], "Hugo")
        self.assertEqual(stderr, "")
        import_module.assert_not_called()

    def test_set_enable_disable_and_reset(self) -> None:
        self.assertEqual(self.run_cli("config", "set", "model", "micro")[0], 0)
        self.assertEqual(self.run_cli("config", "set", "voice", "Luna")[0], 0)
        self.assertEqual(self.run_cli("config", "set", "speed", "1.2")[0], 0)
        self.assertEqual(self.run_cli("disable")[0], 0)
        stored = config.load_config(self.config_path)
        self.assertEqual(
            (stored.enabled, stored.model, stored.voice, stored.speed),
            (False, "micro", "Luna", 1.2),
        )

        self.assertEqual(self.run_cli("config", "reset", "model")[0], 0)
        self.assertEqual(self.run_cli("config", "reset", "voice")[0], 0)
        self.assertEqual(self.run_cli("enable")[0], 0)
        stored = config.load_config(self.config_path)
        self.assertEqual(
            (stored.enabled, stored.model, stored.voice), (True, "mini", "Hugo")
        )

    def test_invalid_value_returns_validation_status(self) -> None:
        invalid_values = (
            ("model", "not-a-model", "Unsupported model"),
            ("voice", "NotAVoice", "Unsupported voice"),
        )

        for key, value, expected_error in invalid_values:
            with self.subTest(key=key):
                result, stdout, stderr = self.run_cli("config", "set", key, value)

                self.assertEqual(result, 2)
                self.assertEqual(stdout, "")
                self.assertIn(expected_error, stderr)

    def test_models_lists_supported_names_and_ids(self) -> None:
        result, stdout, stderr = self.run_cli("models", "--json")

        self.assertEqual(result, 0)
        self.assertEqual(stderr, "")
        self.assertEqual(
            json.loads(stdout),
            {
                "default": "mini",
                "models": {
                    "micro": "KittenML/kitten-tts-micro-0.8",
                    "mini": "KittenML/kitten-tts-mini-0.8",
                    "nano": "KittenML/kitten-tts-nano-0.8-fp32",
                },
            },
        )

    def test_set_model_does_not_load_audio_dependencies(self) -> None:
        with patch.object(cli.importlib, "import_module") as import_module:
            result, _, stderr = self.run_cli("config", "set", "model", "micro")

        self.assertEqual(result, 0)
        self.assertEqual(stderr, "")
        self.assertEqual(config.load_config(self.config_path).model, "micro")
        import_module.assert_not_called()

    def test_audio_test_uses_configured_voice_and_speed(self) -> None:
        config.write_config(
            config.TurnEchoConfig(model="micro", voice="Luna", speed=1.2),
            self.config_path,
        )
        model = Mock()
        model.generate.return_value = object()
        kittentts = Mock()
        kittentts.KittenTTS.return_value = model
        sounddevice = Mock()

        def import_module(name: str):
            return {"kittentts": kittentts, "sounddevice": sounddevice}[name]

        with patch.object(cli.importlib, "import_module", side_effect=import_module):
            result, _, stderr = self.run_cli("test")

        self.assertEqual(result, 0)
        self.assertEqual(stderr, "")
        kittentts.KittenTTS.assert_called_once_with("KittenML/kitten-tts-micro-0.8")
        model.generate.assert_called_once_with(
            cli.TEST_PHRASE,
            voice="Luna",
            speed=1.2,
        )
        sounddevice.play.assert_called_once()
        sounddevice.wait.assert_called_once_with()

    def test_doctor_loads_configured_model(self) -> None:
        config.write_config(
            config.TurnEchoConfig(model="nano"),
            self.config_path,
        )
        kittentts = Mock()
        sounddevice = Mock()
        call_order: list[str] = []
        kittentts.KittenTTS.side_effect = lambda _: call_order.append("model")
        sounddevice.check_output_settings.side_effect = lambda **_: call_order.append(
            "audio"
        )

        def import_module(name: str):
            return {"kittentts": kittentts, "sounddevice": sounddevice}[name]

        with patch.object(cli.importlib, "import_module", side_effect=import_module):
            result, stdout, stderr = self.run_cli("doctor", "--json")

        self.assertEqual(result, 0)
        self.assertEqual(stderr, "")
        self.assertEqual(json.loads(stdout)["model"], "nano")
        kittentts.KittenTTS.assert_called_once_with("KittenML/kitten-tts-nano-0.8-fp32")
        sounddevice.check_output_settings.assert_called_once_with(samplerate=24000)
        self.assertEqual(call_order, ["audio", "model"])


if __name__ == "__main__":
    unittest.main()
