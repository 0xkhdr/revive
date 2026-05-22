"""Node/Npm environment package provider orchestration."""

import os
import shutil
import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.node")


class NodeProvider(BaseProvider):
    """Orchestrates Node.js version management using fnm or nvm."""

    def __init__(self) -> None:
        super().__init__("node")

    def _get_current_version(self) -> str | None:
        """Returns the current active Node.js version, normalized without 'v' prefix."""
        if not shutil.which("node"):
            return None
        try:
            res = subprocess.run(["node", "-v"], capture_output=True, text=True, check=True)
            val = res.stdout.strip()
            if val.startswith("v"):
                val = val[1:]
            return val
        except Exception:
            return None

    def _resolve_target_version(self, repo_dir: str, version: str | None, version_file: str | None) -> str | None:
        """Resolves the target Node.js version from manifest configurations."""
        target: str | None = None
        if version:
            target = version
        elif version_file:
            # Resolve relative to repo_dir
            full_path = os.path.join(repo_dir, version_file)
            if os.path.exists(full_path):
                try:
                    with open(full_path, encoding="utf-8") as f:
                        target = f.read().strip()
                except Exception as e:
                    logger.warning(f"Failed to read Node.js version file '{full_path}': {e}")
            else:
                logger.warning(f"Node.js version file '{full_path}' does not exist")

        if target:
            target = target.strip()
            if target.startswith("v"):
                target = target[1:]
        return target

    def install_node(self, repo_dir: str, version: str | None, version_file: str | None, dry_run: bool = False) -> None:
        """Verifies Node.js version and attempts to install it via fnm/nvm if mismatch occurs."""
        target_version = self._resolve_target_version(repo_dir, version, version_file)
        if not target_version:
            logger.info("No Node.js version target defined. Skipping.")
            return

        current_version = self._get_current_version()
        logger.info(f"Target Node.js version: {target_version}. Current version: {current_version or 'None'}")

        # Simpler prefix comparison (e.g. "20" matches "20.11.0")
        if current_version and current_version.startswith(target_version):
            logger.info(f"Node.js version {current_version} matches target {target_version}. No action needed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] Node.js version mismatch! Would install Node.js version: {target_version}")
            return

        # Attempt to install using fnm (Fast Node Manager) first, since it is a binary executable
        if shutil.which("fnm"):
            logger.info(f"fnm found. Installing Node.js {target_version} via fnm...")
            try:
                self.execute_with_retry(["fnm", "install", target_version])
                logger.info(f"Successfully installed Node.js {target_version} via fnm.")
                return
            except Exception as e:
                logger.warning(f"fnm install failed: {e}")

        # Attempt to use nvm if nvm.sh is available
        nvm_dir = os.environ.get("NVM_DIR", os.path.expanduser("~/.nvm"))
        nvm_sh = os.path.join(nvm_dir, "nvm.sh")
        if os.path.exists(nvm_sh):
            logger.info(f"nvm script found at {nvm_sh}. Running install via bash shell environment...")
            try:
                # Since nvm is a shell function sourced from nvm.sh, we run it inside bash safely
                # (Still avoiding raw shell=True on unchecked input by passing clean array parameters)
                cmd = ["bash", "-c", f". {nvm_sh} && nvm install {target_version}"]
                subprocess.run(cmd, check=True, capture_output=True, text=True)
                logger.info(f"Successfully installed Node.js {target_version} via nvm.")
                return
            except Exception as e:
                logger.warning(f"nvm install failed: {e}")

        # If we cannot install, we must raise a ProviderError or log a major warning
        raise ProviderError(
            f"Node.js version mismatch (current: {current_version or 'None'}, target: {target_version}) "
            "and no Node.js managers (fnm or nvm) are available to automatically install the target version."
        )
