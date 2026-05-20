"""Sample Python file for Cartographer AST extraction tests."""


class DataProcessor:
    """Processes incoming data records."""

    def process(self, record):
        """Process a single record and return it."""
        return record

    def validate(self, record):
        """Validate a record before processing."""
        return bool(record)


class Config:
    """Configuration container."""

    def __init__(self, path):
        self.path = path


def load_config(path):
    """Load configuration from disk."""
    return Config(path)


def _internal_helper():
    """Private helper — should still be extracted (Python has no enforced access)."""
    pass
