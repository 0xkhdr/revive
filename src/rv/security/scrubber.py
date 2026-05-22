"""SecretScrubber to filter and redact sensitive data from logs and traces."""

import logging
import re


class SecretScrubber:
    """Detects and redacts secrets from text using regex and a dynamic registry."""

    # Static patterns for common secrets like Age secret keys or typical high-entropy tokens
    _static_patterns: list[re.Pattern[str]] = [
        re.compile(r"AGE-SECRET-KEY-1[a-zA-Z0-9]+", re.IGNORECASE),
        re.compile(r"(?:ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp256)\s+[a-zA-Z0-9+/=]+", re.IGNORECASE),
        re.compile(
            r"-----BEGIN\s+(?:RSA|OPENSSH|PRIVATE)\s+KEY-----[^-]+-----END\s+(?:RSA|OPENSSH|PRIVATE)\s+KEY-----",
            re.DOTALL,
        ),
    ]

    _dynamic_secrets: set[str] = set()

    @classmethod
    def register_secret(cls, secret: str) -> None:
        """Dynamically registers a plaintext secret to be scrubbed from any output."""
        if secret and len(secret) > 4:  # Only register reasonably long strings to avoid false positives
            cls._dynamic_secrets.add(secret)

    @classmethod
    def clear_dynamic_secrets(cls) -> None:
        """Clears all dynamically registered secrets."""
        cls._dynamic_secrets.clear()

    @classmethod
    def scrub(cls, text: str) -> str:
        """Redacts all registered and matching secrets from the input text."""
        if not text:
            return text

        scrubbed = text

        # 1. Scrub static patterns
        for pattern in cls._static_patterns:
            scrubbed = pattern.sub("[REDACTED]", scrubbed)

        # 2. Scrub dynamic secrets (sorted by length descending to prevent partial redacts)
        sorted_secrets = sorted(cls._dynamic_secrets, key=len, reverse=True)
        for secret in sorted_secrets:
            # Escape the secret to use in replace safely
            scrubbed = scrubbed.replace(secret, "[REDACTED]")

        return scrubbed


class ScrubbingFormatter(logging.Formatter):
    """Logging Formatter that automatically scrubs sensitive data from log records."""

    def format(self, record: logging.LogRecord) -> str:
        formatted = super().format(record)
        return SecretScrubber.scrub(formatted)
