import io
import sys
import tempfile
import unittest
from contextlib import redirect_stderr
from pathlib import Path
from unittest.mock import Mock, call, patch

PROJECT_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(PROJECT_ROOT / "src"))

from turnecho import worker  # noqa: E402
from turnecho.config import TurnEchoConfig  # noqa: E402
from turnecho.constant import TurnEchoJobProcessingStatus  # noqa: E402
from turnecho.schema import TurnEchoJob  # noqa: E402


class WorkerTests(unittest.TestCase):
    @staticmethod
    def make_job(job_id: str) -> TurnEchoJob:
        return TurnEchoJob(
            id=job_id,
            host="codex",
            session_id="session-1",
            turn_id=f"turn-{job_id}",
            message=f"Summary for {job_id}.",
            voice="Luna",
            speed=1.2,
            processing_status=TurnEchoJobProcessingStatus.PROCESSING.value,
            created_at=1,
            started_at=2,
        )

    def test_worker_exits_before_model_load_when_queue_is_empty(self) -> None:
        lock_file = Mock()

        with (
            patch.object(worker, "acquire_worker_lock", return_value=lock_file),
            patch.object(worker, "requeue_processing_jobs_from_db") as requeue_jobs,
            patch.object(worker, "has_pending_jobs_in_db", return_value=False),
        ):
            worker.process()

        requeue_jobs.assert_called_once_with()
        lock_file.close.assert_called_once_with()

    def test_worker_retries_lock_before_exiting(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            lock_path = str(Path(directory) / "worker.lock")
            with (
                patch.object(
                    worker, "TURNECHO_WORKER_LOCK_FILE_PATH_MACOS_LINUX", lock_path
                ),
                patch.object(
                    worker.fcntl,
                    "flock",
                    side_effect=[BlockingIOError(), None],
                ) as flock,
                patch.object(worker.time, "monotonic", side_effect=[0.0, 0.25]),
                patch.object(worker.time, "sleep") as sleep,
            ):
                lock_file = worker.acquire_worker_lock()

            lock_file.close()

        self.assertEqual(flock.call_count, 2)
        sleep.assert_called_once_with(0.25)

    def test_run_loop_completes_claimed_job(self) -> None:
        job = self.make_job("job-1")
        model = Mock()
        model.generate.return_value = object()
        model_factory = Mock(return_value=model)
        audio_output = Mock()

        with (
            patch.object(worker, "claim_next_job_from_db", side_effect=[job, None]),
            patch.object(worker, "load_config", return_value=TurnEchoConfig()),
            patch.object(worker, "update_job_db", return_value=True) as update_job,
            patch.object(worker, "TURNECHO_WORKER_IDLE_TIMEOUT_WITHOUT_JOB_SECONDS", 0),
        ):
            worker.run_in_loop(model_factory, audio_output)

        model_factory.assert_called_once_with("KittenML/kitten-tts-mini-0.8")
        model.generate.assert_called_once_with(
            job.message,
            voice="Luna",
            speed=1.2,
        )
        audio_output.play.assert_called_once()
        audio_output.wait.assert_called_once_with()
        update_job.assert_called_once_with(job)
        self.assertEqual(
            job.processing_status,
            TurnEchoJobProcessingStatus.SUCCESS.value,
        )
        self.assertIsNotNone(job.completed_at)

    def test_run_loop_reuses_and_switches_models_in_queue_order(self) -> None:
        first_job = self.make_job("job-1")
        second_job = self.make_job("job-2")
        third_job = self.make_job("job-3")
        mini_model = Mock()
        mini_model.generate.return_value = object()
        micro_model = Mock()
        micro_model.generate.return_value = object()
        model_factory = Mock(side_effect=[mini_model, micro_model])

        with (
            patch.object(
                worker,
                "claim_next_job_from_db",
                side_effect=[first_job, second_job, third_job, None],
            ),
            patch.object(
                worker,
                "load_config",
                side_effect=[
                    TurnEchoConfig(model="mini"),
                    TurnEchoConfig(model="mini"),
                    TurnEchoConfig(model="micro"),
                ],
            ),
            patch.object(worker, "update_job_db", return_value=True),
            patch.object(worker, "TURNECHO_WORKER_IDLE_TIMEOUT_WITHOUT_JOB_SECONDS", 0),
        ):
            worker.run_in_loop(model_factory, Mock())

        self.assertEqual(
            model_factory.call_args_list,
            [
                call("KittenML/kitten-tts-mini-0.8"),
                call("KittenML/kitten-tts-micro-0.8"),
            ],
        )
        self.assertEqual(mini_model.generate.call_count, 2)
        micro_model.generate.assert_called_once_with(
            third_job.message,
            voice=third_job.voice,
            speed=third_job.speed,
        )

    def test_model_load_failure_marks_job_failed(self) -> None:
        job = self.make_job("job-1")
        model_factory = Mock(side_effect=RuntimeError("model unavailable"))

        with redirect_stderr(io.StringIO()):
            with (
                patch.object(worker, "claim_next_job_from_db", side_effect=[job, None]),
                patch.object(worker, "load_config", return_value=TurnEchoConfig()),
                patch.object(worker, "update_job_db", return_value=True) as update_job,
                patch.object(
                    worker, "TURNECHO_WORKER_IDLE_TIMEOUT_WITHOUT_JOB_SECONDS", 0
                ),
            ):
                worker.run_in_loop(model_factory, Mock())

        update_job.assert_called_once_with(job)
        self.assertEqual(
            job.processing_status,
            TurnEchoJobProcessingStatus.FAILED.value,
        )
        self.assertEqual(job.error_message, "model unavailable")


if __name__ == "__main__":
    unittest.main()
