class LifecycleError(Exception):
    """Нештатное завершение lifecycle процесса; приводит к non-zero exit."""


class DrainTimeoutError(LifecycleError):
    """Начатая работа не завершилась до shutdown deadline."""
