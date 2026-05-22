"""Path canonicalization, cross-device, and symlink loop detection utilities.
"""

import os


class PathHelper:
    """Helper class for path verification, loop detection, and system transitions."""

    @staticmethod
    def canonicalize(path: str) -> str:
        """Fully resolves environment variables, expands user directories, and returns absolute path.

        Does not resolve symlinks if we need to preserve them as target paths.
        """
        expanded = os.path.expanduser(os.path.expandvars(path))
        return os.path.abspath(expanded)

    @staticmethod
    def is_cross_device(path1: str, path2: str) -> bool:
        """Determines if path1 and path2 lie on different physical filesystems/devices.

        This is vital for atomic renames and symlink fallbacks.
        """
        # If any path doesn't exist, we check their parent directories
        p1 = path1
        while p1 and not os.path.exists(p1):
            parent = os.path.dirname(p1)
            if parent == p1:
                break
            p1 = parent

        p2 = path2
        while p2 and not os.path.exists(p2):
            parent = os.path.dirname(p2)
            if parent == p2:
                break
            p2 = parent

        try:
            dev1 = os.stat(p1).st_dev
            dev2 = os.stat(p2).st_dev
            return dev1 != dev2
        except Exception:
            # Fallback to conservative true if we can't stat
            return False

    @classmethod
    def detect_symlink_loop(cls, path: str, visited: set[str] | None = None) -> bool:
        """Recursively checks if a symlink structure creates a cyclic loop.

        Returns True if a loop is detected.
        """
        if visited is None:
            visited = set()

        canonical_path = cls.canonicalize(path)

        if not os.path.islink(canonical_path):
            return False

        if canonical_path in visited:
            return True

        visited.add(canonical_path)

        try:
            link_target = os.readlink(canonical_path)
            # Resolve relative links relative to the symlink's directory
            if not os.path.isabs(link_target):
                link_target = os.path.join(os.path.dirname(canonical_path), link_target)
            return cls.detect_symlink_loop(link_target, visited)
        except Exception:
            return False
        finally:
            visited.remove(canonical_path)

    @staticmethod
    def is_safe_subpath(base_dir: str, target_path: str) -> bool:
        """Verifies that target_path is nested inside base_dir to prevent path traversal outside repo."""
        real_base = os.path.realpath(base_dir)
        real_target = os.path.realpath(target_path)
        return real_target.startswith(real_base + os.sep) or real_target == real_base
