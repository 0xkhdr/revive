"""Environment variable interpolator with strict checks and defaults.
"""

import os
import re


class Interpolator:
    """Safely substitutes ${VAR} or ${VAR:-default} from environment variables."""

    # Regex matches ${VAR} or ${VAR:-default_value}
    _pattern = re.compile(r"\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::-([^}]+))?\}")

    @classmethod
    def interpolate(cls, text: str, env_override: dict[str, str] | None = None) -> str:
        """Interpolates environment variables in the provided text.

        Args:
            text: String containing interpolation expressions like ${HOME}.
            env_override: Optional dictionary of environment variables to use instead of os.environ.

        Returns:
            The interpolated string.

        Raises:
            ValueError: If a variable is missing and no default is provided.
        """
        env = env_override if env_override is not None else os.environ

        def replacer(match: re.Match[str]) -> str:
            var_name = match.group(1)
            default_val = match.group(2)

            if var_name in env:
                return env[var_name]

            if default_val is not None:
                return default_val

            raise ValueError(f"Environment variable '{var_name}' is required but not set, and no default was provided")

        try:
            return cls._pattern.sub(replacer, text)
        except ValueError as e:
            raise ValueError(f"Interpolation failed: {e}") from e
