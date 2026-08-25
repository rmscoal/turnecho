class WorkerAlreadyRunning(Exception):
    pass


class InstallError(RuntimeError):
    """Raised when the GitHub plugin installation cannot finish safely."""
