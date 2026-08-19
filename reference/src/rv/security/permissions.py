"""PermissionEnforcer to securely validate and enforce file permissions."""

import os


class PermissionEnforcer:
    """Safely validates and applies file/directory permissions and ownership."""

    @staticmethod
    def _is_windows() -> bool:
        """Helper to determine if running on Windows, allowing clean test mocking."""
        return os.name == "nt"

    @staticmethod
    def enforce(path: str, permissions: str, owner: str | None = None) -> None:
        """Applies permissions and ownership to a target file or directory.

        Args:
            path: Absolute target path.
            permissions: Octal permission string (e.g., "0644" or "0600").
            owner: Optional owner username to set.

        Raises:
            ValueError: If the permission string is invalid.
            PermissionError: If unable to apply permissions/ownership.
        """
        if not os.path.exists(path):
            raise FileNotFoundError(f"Cannot enforce permissions on non-existent path: {path}")

        # Parse octal permissions
        try:
            mode = int(permissions, 8)
        except ValueError as e:
            raise ValueError(f"Invalid octal permissions: {permissions}") from e

        # Enforce chmod
        try:
            if PermissionEnforcer._is_windows():
                import logging
                import stat

                try:
                    # Owner read bit (0o400) vs owner write bit (0o200)
                    if (mode & 0o200) == 0:
                        os.chmod(path, stat.S_IREAD)
                    else:
                        os.chmod(path, stat.S_IWRITE)
                    logging.getLogger("rv.security.permissions").warning(
                        f"Mapped POSIX permissions '{permissions}' to Windows attributes on {path}"
                    )
                except Exception as e:
                    logging.getLogger("rv.security.permissions").warning(
                        f"Failed to map POSIX permissions '{permissions}' on Windows for {path}: {e}"
                    )
            else:
                os.chmod(path, mode)
        except Exception as e:
            raise PermissionError(f"Failed to change permissions for {path} to {permissions}: {e}") from e

        # Enforce chown if requested and possible
        if owner:
            try:
                if PermissionEnforcer._is_windows():
                    import logging

                    logging.getLogger("rv.security.permissions").warning(
                        f"Ownership configuration (chown) is only supported on UNIX/POSIX platforms. Skipped setting owner '{owner}' on {path}."
                    )
                    return

                import pwd

                pw = pwd.getpwnam(owner)
                uid = pw.pw_uid
                gid = pw.pw_gid
                os.chown(path, uid, gid)
            except ImportError:
                raise PermissionError("Owner configuration (chown) is only supported on UNIX/POSIX platforms")
            except KeyError:
                raise ValueError(f"User '{owner}' does not exist on this system")
            except Exception as e:
                # Often chown requires superuser privileges; raise PermissionError if it fails
                raise PermissionError(f"Failed to change ownership of {path} to {owner}: {e}") from e

    @staticmethod
    def verify(path: str, permissions: str) -> bool:
        """Checks if a target path matches the expected octal permissions.

        Returns:
            True if permissions match, False otherwise.
        """
        if not os.path.exists(path):
            return False

        try:
            if PermissionEnforcer._is_windows():
                import logging

                logging.getLogger("rv.security.permissions").warning(
                    f"Permissions verification bypassed on Windows for {path}"
                )
                return True

            expected_mode = int(permissions, 8) & 0o7777
            actual_mode = os.stat(path).st_mode & 0o7777
            return expected_mode == actual_mode
        except Exception:
            return False
