"""Sandbox execution runner. Executes plugins in isolated subprocesses.
"""

import base64
import json
import os
import subprocess
import sys
from typing import Any

from rv.plugins.context import ReviveContext
from rv.plugins.loader import Plugin


class SandboxRunner:
    """Orchestrates secure subprocess-based execution of plugins under sandboxed constraints."""

    @staticmethod
    def run_plugin(plugin: Plugin, context: ReviveContext) -> dict[str, Any]:
        """Executes a plugin inside a sandboxed subprocess.

        Args:
            plugin: The loaded Plugin instance.
            context: The current execution ReviveContext.

        Returns:
            A dictionary containing the parsed JSON stdout or status details.
        """
        # Serialize permissions & context
        perms_dict = plugin.manifest.permissions.model_dump()
        perms_json = json.dumps(perms_dict)
        perms_b64 = base64.b64encode(perms_json.encode("utf-8")).decode("utf-8")

        ctx_dict = context.model_dump()
        ctx_json = json.dumps(ctx_dict)
        ctx_b64 = base64.b64encode(ctx_json.encode("utf-8")).decode("utf-8")

        # Constrain timeout to [1, 300] seconds. Mandatory default is 30s.
        timeout = 30
        if plugin.manifest.timeout:
            timeout = min(max(1, plugin.manifest.timeout), 300)

        # Build execution command using sys.executable to run module sandbox_wrapper
        cmd = [
            sys.executable,
            "-m",
            "rv.plugins.sandbox_wrapper",
            plugin.entrypoint_path,
            perms_b64,
            ctx_b64,
            context.hook_type
        ]

        # Prepare isolated environment
        env = os.environ.copy()
        if not plugin.manifest.permissions.network:
            # Inject standard environment blocks for network access
            env["http_proxy"] = "http://127.0.0.1:0"
            env["https_proxy"] = "http://127.0.0.1:0"
            env["no_proxy"] = "*"

        try:
            result = subprocess.run(
                cmd,
                capture_output=True,
                env=env,
                timeout=timeout,
                check=False
            )

            stdout_str = result.stdout.decode("utf-8", errors="replace").strip()
            stderr_str = result.stderr.decode("utf-8", errors="replace").strip()

            if result.returncode != 0:
                raise RuntimeError(
                    f"Plugin '{plugin.manifest.name}' execution failed with exit code {result.returncode}.\n"
                    f"Stderr: {stderr_str}\n"
                    f"Stdout: {stdout_str}"
                )

            # Try parsing stdout as JSON
            try:
                if stdout_str:
                    return json.loads(stdout_str)  # type: ignore[no-any-return]
            except Exception:
                pass

            return {
                "status": "success",
                "message": "Plugin completed successfully",
                "stdout": stdout_str
            }

        except subprocess.TimeoutExpired as e:
            raise TimeoutError(
                f"Plugin '{plugin.manifest.name}' timed out after {timeout} seconds."
            ) from e
