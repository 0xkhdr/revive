"""Docker package provider orchestration."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.docker")


class DockerProvider(BaseProvider):
    """Orchestrates Docker images via docker pull."""

    def __init__(self) -> None:
        super().__init__("docker")

    def is_installed(self, pkg: str) -> bool:
        """Checks if a Docker image is available locally.

        Args:
            pkg: Docker image name (e.g. 'postgres:latest').

        Returns:
            True if image is present locally, False otherwise.
        """
        try:
            res = subprocess.run(["docker", "image", "inspect", pkg], capture_output=True, check=False)
            return res.returncode == 0
        except Exception:
            return False

    def _is_image_local(self, image: str) -> bool:
        """Checks if a docker image is available locally via docker image inspect."""
        return self.is_installed(image)

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Pulls missing Docker images.

        Args:
            packages: List of docker images to pull (e.g. 'postgres:latest').
            dry_run: Whether to simulate orchestration.
            use_cache: Unused for Docker (image presence checked via local daemon). Kept for interface parity.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("Docker CLI ('docker') is not installed or not in system PATH")

        missing = []
        for img in packages:
            if not self._is_image_local(img):
                missing.append(img)

        if not missing:
            logger.info("All docker images are already present locally.")
            return

        if dry_run:
            logger.info(f"[Dry Run] Docker images would be pulled: {', '.join(missing)}")
            return

        logger.info(f"Pulling docker images: {', '.join(missing)}")
        for img in missing:
            try:
                self.execute_with_retry(["docker", "pull", img])
                logger.info(f"Successfully pulled image: {img}")
            except Exception as e:
                raise ProviderError(f"Docker pull failed for {img}: {e}") from e
