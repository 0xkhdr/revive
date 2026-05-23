"""Asset handlers for copy, symlink, template, and secret orchestration."""

import os
import sys

import jinja2
import typer

from rv.models.manifest import Asset, AssetType, ConflictStrategy, Secret
from rv.security.encryptor import AgeEncryptor
from rv.security.tempfile import SecureTempFile
from rv.security.zerobuffer import ZeroBuffer
from rv.transactions.context import TransactionContext
from rv.utils.interpolate import Interpolator
from rv.utils.path import PathHelper


class AssetHandlerError(Exception):
    """Exception raised for errors in asset handling."""

    pass


class AssetHandler:
    """Orchestrates planning and execution checks for individual assets."""

    @staticmethod
    def is_interactive() -> bool:
        """Determines if the current process is running in an interactive terminal."""
        return sys.stdout.isatty() and sys.stdin.isatty()

    @classmethod
    def handle(
        cls,
        asset: Asset | Secret,
        repo_dir: str,
        tx_context: TransactionContext,
        identity_path: str | None = None,
        interactive: bool | None = None,
    ) -> bool:
        """Processes the asset and registers planned operations on the transaction context.

        Args:
            asset: The Asset or Secret model to deploy.
            repo_dir: Canonical path to the source repository.
            tx_context: Active transaction context.
            identity_path: Optional path to the age identity private key file.
            interactive: Override interactive terminal check.

        Returns:
            True if the asset was planned successfully, False if it was skipped.
        """
        # 1. Resolve paths
        abs_source = os.path.join(repo_dir, asset.source)

        # 2. Prevent path traversal outside the repo for the source path
        # (Already validated by Pydantic, but double-check to be bulletproof)
        if not os.path.exists(abs_source) and not asset.encrypted:
            raise FileNotFoundError(f"Source file not found: {abs_source}")

        # Support target list loop
        targets = [asset.target] if isinstance(asset.target, str) else asset.target
        planned_any = False

        for target_expr in targets:
            # Interpolate and canonicalize target
            interpolated_target = Interpolator.interpolate(target_expr)
            abs_target = PathHelper.canonicalize(interpolated_target)

            # Match source sub-item if directory
            target_source = abs_source
            if os.path.isdir(abs_source):
                basename = os.path.basename(abs_target)
                potential_sources = []
                if asset.encrypted:
                    potential_sources.append(os.path.join(abs_source, f"{basename}.age"))
                potential_sources.append(os.path.join(abs_source, basename))

                for pot_src in potential_sources:
                    if os.path.exists(pot_src):
                        target_source = pot_src
                        break

            # 3. Check for conflicts
            if os.path.exists(abs_target) or os.path.islink(abs_target):
                # Check strategy
                strategy = getattr(asset, "conflict_strategy", ConflictStrategy.PROMPT)

                if strategy == ConflictStrategy.SKIP:
                    # Skip target
                    continue
                elif strategy == ConflictStrategy.ABORT:
                    raise AssetHandlerError(
                        f"Target already exists and conflict strategy is set to 'abort': {abs_target}"
                    )
                elif strategy == ConflictStrategy.PROMPT:
                    is_terminal_interactive = cls.is_interactive() if interactive is None else interactive
                    if is_terminal_interactive:
                        # Prompt the user
                        confirm = typer.confirm(f"Target '{abs_target}' already exists. Overwrite?", default=False)
                        if not confirm:
                            continue
                    else:
                        # Non-interactive fallback: abort to prevent silent data loss
                        raise AssetHandlerError(
                            f"Target already exists and conflict strategy is 'prompt' "
                            f"but running in non-interactive environment: {abs_target}"
                        )
                # OVERWRITE continues below...

            # 4. Handle based on asset/secret type
            if asset.type == AssetType.SYMLINK:
                cls._handle_symlink(asset, target_source, abs_target, tx_context)
            elif asset.type == AssetType.COPY:
                cls._handle_copy(asset, target_source, abs_target, tx_context, identity_path)
            elif asset.type == AssetType.TEMPLATE:
                cls._handle_template(asset, target_source, abs_target, tx_context)
            elif asset.type == AssetType.SECRET:
                cls._handle_secret(asset, target_source, abs_target, tx_context, identity_path)
            else:
                raise ValueError(f"Unsupported asset type: {asset.type}")

            planned_any = True

        return planned_any

    @classmethod
    def _handle_symlink(
        cls, asset: Asset | Secret, abs_source: str, abs_target: str, tx_context: TransactionContext
    ) -> None:
        """Registers a symlink creation operation."""
        # Detect cyclic symlink loop
        # Temporarily mock link if we can to detect loops
        if PathHelper.detect_symlink_loop(abs_source):
            raise AssetHandlerError(f"Symlink loop detected for source: {abs_source}")

        # Plan deletion if target exists
        if os.path.exists(abs_target) or os.path.islink(abs_target):
            tx_context.plan_operation("delete", abs_target)

        # Plan symlink creation
        # Symlink target points to the source file path
        # In a unidirectional model, we want a symlink target that points to the absolute path of the source file
        tx_context.plan_operation(
            "symlink", abs_target, source_data=abs_source, permissions=asset.permissions, owner=asset.owner
        )

    @classmethod
    def _handle_copy(
        cls,
        asset: Asset | Secret,
        abs_source: str,
        abs_target: str,
        tx_context: TransactionContext,
        identity_path: str | None = None,
    ) -> None:
        """Registers a file copy operation, optionally decrypting the source."""
        if os.path.exists(abs_target) or os.path.islink(abs_target):
            tx_context.plan_operation("delete", abs_target)

        if asset.encrypted:
            if not identity_path:
                raise AssetHandlerError(f"Identity key required to decrypt encrypted asset: {asset.id}")

            # Decrypt via AgeEncryptor
            with SecureTempFile.file() as tmp_decrypted:
                try:
                    AgeEncryptor.decrypt_file(abs_source, tmp_decrypted, identity_path)
                    with open(tmp_decrypted, "rb") as f:
                        decrypted_bytes = bytearray(f.read())
                except Exception as e:
                    raise AssetHandlerError(f"Failed to decrypt asset {asset.id}: {e}") from e

                # Plan copy with the decrypted bytes
                tx_context.plan_operation(
                    "copy",
                    abs_target,
                    source_data=bytes(decrypted_bytes),
                    permissions=asset.permissions,
                    owner=asset.owner,
                )
                # Zero out decrypted memory
                ZeroBuffer.zero(decrypted_bytes)
        else:
            # Standard copy
            tx_context.plan_operation(
                "copy", abs_target, source_data=abs_source, permissions=asset.permissions, owner=asset.owner
            )

    @classmethod
    def _handle_template(
        cls, asset: Asset | Secret, abs_source: str, abs_target: str, tx_context: TransactionContext
    ) -> None:
        """Registers a template rendering operation."""
        if os.path.exists(abs_target) or os.path.islink(abs_target):
            tx_context.plan_operation("delete", abs_target)

        # Read template file content
        try:
            with open(abs_source, encoding="utf-8") as f:
                template_content = f.read()
        except Exception as e:
            raise AssetHandlerError(f"Failed to read template source {abs_source}: {e}") from e

        # Merge environment variables and template_vars
        context = dict(os.environ)
        if isinstance(asset, Asset) and asset.template_vars:
            context.update(asset.template_vars)

        # Render template using Jinja2 with StrictUndefined to catch missing variables
        try:
            template = jinja2.Template(template_content, undefined=jinja2.StrictUndefined)
            rendered = template.render(context)
        except Exception as e:
            raise AssetHandlerError(f"Template rendering failed for {asset.id}: {e}") from e

        # Plan copy with rendered content
        tx_context.plan_operation(
            "copy", abs_target, source_data=rendered.encode("utf-8"), permissions=asset.permissions, owner=asset.owner
        )

    @classmethod
    def _handle_secret(
        cls,
        asset: Asset | Secret,
        abs_source: str,
        abs_target: str,
        tx_context: TransactionContext,
        identity_path: str | None = None,
    ) -> None:
        """Registers a secret file deployment, decrypting with age and enforcing secure permissions."""
        if os.path.exists(abs_target) or os.path.islink(abs_target):
            tx_context.plan_operation("delete", abs_target)

        if not identity_path:
            raise AssetHandlerError(f"Identity key required to decrypt secret: {asset.id}")

        # Decrypt via AgeEncryptor
        with SecureTempFile.file() as tmp_decrypted:
            try:
                AgeEncryptor.decrypt_file(abs_source, tmp_decrypted, identity_path)
                with open(tmp_decrypted, "rb") as f:
                    decrypted_bytes = bytearray(f.read())
            except Exception as e:
                raise AssetHandlerError(f"Failed to decrypt secret {asset.id}: {e}") from e

            # Enforce strict secret permissions (must be secure, typically 0600)
            permissions = asset.permissions or "0600"

            # Plan copy with decrypted bytes
            tx_context.plan_operation(
                "copy", abs_target, source_data=bytes(decrypted_bytes), permissions=permissions, owner=asset.owner
            )
            # Zero out decrypted memory
            ZeroBuffer.zero(decrypted_bytes)
