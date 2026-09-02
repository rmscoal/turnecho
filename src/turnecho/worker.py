"""Run one global TurnEcho worker across all host sessions.

The worker owns the process lock, recovers abandoned jobs, keeps the TTS model
loaded, and processes queued messages sequentially.
"""

import fcntl
import subprocess
import sys
import time
from pathlib import Path

from .config import load_config
from .constant import (
    TURNECHO_AUDIO_SAMPLE_RATE,
    TURNECHO_MODEL_IDS,
    TURNECHO_WORKER_IDLE_TIMEOUT_WITHOUT_JOB_SECONDS,
    TURNECHO_WORKER_LOCK_FILE_PATH_MACOS_LINUX,
    TURNECHO_WORKER_LOCK_RETRY_SECONDS,
    TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX,
    TURNECHO_WORKER_POLL_INTERVAL_SECONDS,
    TurnEchoJobProcessingStatus,
)
from .exc import WorkerAlreadyRunning
from .sqlite import (
    claim_next_job_from_db,
    has_pending_jobs_in_db,
    requeue_processing_jobs_from_db,
    update_job_db,
)


def spawn_background_worker() -> None:
    log_path = Path(TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX).expanduser()
    log_path.parent.mkdir(parents=True, exist_ok=True)

    worker_command = [sys.executable, "-m", "turnecho.worker"]

    with log_path.open("ab") as log_file:
        subprocess.Popen(
            worker_command,
            stdin=subprocess.DEVNULL,
            stdout=log_file,
            stderr=subprocess.STDOUT,
            start_new_session=True,
            close_fds=True,
        )


def acquire_worker_lock():
    lock_path = Path(TURNECHO_WORKER_LOCK_FILE_PATH_MACOS_LINUX).expanduser()
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    lock_file = lock_path.open("a+")

    retry_deadline = time.monotonic() + TURNECHO_WORKER_LOCK_RETRY_SECONDS

    while True:
        try:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            return lock_file
        except BlockingIOError as error:
            remaining_seconds = retry_deadline - time.monotonic()
            if remaining_seconds <= 0:
                lock_file.close()
                raise WorkerAlreadyRunning from error

            time.sleep(min(TURNECHO_WORKER_POLL_INTERVAL_SECONDS, remaining_seconds))


def run_in_loop(model_factory, audio_output):
    last_activity = time.monotonic()
    model = None
    loaded_model_id = None

    while True:
        job = claim_next_job_from_db()

        if job is None:
            if (
                time.monotonic() - last_activity
                >= TURNECHO_WORKER_IDLE_TIMEOUT_WITHOUT_JOB_SECONDS
            ):
                # Exit worker's whole process
                return

            time.sleep(TURNECHO_WORKER_POLL_INTERVAL_SECONDS)
            continue

        last_activity = time.monotonic()

        # Generate audio and update job record accordingly
        try:
            config = load_config()
            requested_model_id = TURNECHO_MODEL_IDS[config.model]
            if requested_model_id != loaded_model_id:
                model = None
                loaded_model_id = None
                model = model_factory(requested_model_id)
                loaded_model_id = requested_model_id

            audio = model.generate(
                job.message,
                voice=job.voice,
                speed=job.speed,
            )
            audio_output.play(audio, samplerate=TURNECHO_AUDIO_SAMPLE_RATE)
            audio_output.wait()

            job.processing_status = TurnEchoJobProcessingStatus.SUCCESS.value
            job.completed_at = int(time.time())
            job.error_message = None
        except Exception as e:
            print(e, file=sys.stderr)
            job.processing_status = TurnEchoJobProcessingStatus.FAILED.value
            job.completed_at = int(time.time())
            job.error_message = str(e)

        update_job_db(job)


def process():
    worker_lock = acquire_worker_lock()

    try:
        requeue_processing_jobs_from_db()
        if not has_pending_jobs_in_db():
            return

        # Import KittenTTS only inside the subprocess
        import sounddevice
        from kittentts import KittenTTS

        run_in_loop(KittenTTS, sounddevice)
    finally:
        worker_lock.close()


if __name__ == "__main__":
    try:
        process()
    except WorkerAlreadyRunning:
        pass
