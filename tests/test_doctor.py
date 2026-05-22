"""Test suite for DoctorService diagnostics and health checking.
"""

import os
import shutil
import tempfile
import pytest
from unittest.mock import patch, MagicMock

from rv.models.manifest import Asset, AssetType
from rv.services.doctor import DoctorService


def test_doctor_check_health_corrupt_manifest() -> None:
    """Tests check_health with a corrupt or invalid manifest.yaml."""
    temp_dir = tempfile.mkdtemp()
    try:
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write("invalid: yaml: [ [ :")

        report = DoctorService.check_health(temp_dir)
        assert report["healthy"] is False
        assert any("Failed to load or validate manifest.yaml" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)


def test_doctor_check_health_corrupt_lockfile() -> None:
    """Tests check_health with an invalid manifest.lock JSON."""
    temp_dir = tempfile.mkdtemp()
    try:
        # Create a valid manifest first so we don't fail manifest check
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write("version: 2\nassets: []\nprofiles: {}\n")

        # Create an invalid lockfile
        lockfile_path = os.path.join(temp_dir, "manifest.lock")
        with open(lockfile_path, "w", encoding="utf-8") as f:
            f.write("{invalid json")

        report = DoctorService.check_health(temp_dir)
        # Lockfile error is warning severity, so repo can still be 'healthy'
        assert report["healthy"] is True
        assert any("manifest.lock is corrupt or invalid" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)


def test_doctor_check_health_no_encryption_tools() -> None:
    """Tests check_health behavior when neither age CLI nor pyrage library is available."""
    temp_dir = tempfile.mkdtemp()
    try:
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write("version: 2\nassets: []\nprofiles: {}\n")

        with patch("rv.utils.platform.Platform.has_tool", return_value=False), \
             patch("rv.security.encryptor.AgeEncryptor.is_pyrage_available", return_value=False):
            report = DoctorService.check_health(temp_dir)
            assert any("Neither pyrage python library nor" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)


def test_doctor_check_health_nonexistent_profile() -> None:
    """Tests check_health with a nonexistent profile name."""
    temp_dir = tempfile.mkdtemp()
    try:
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write("version: 2\nassets: []\nprofiles: {\"base\": {\"extends\": []}}\n")

        report = DoctorService.check_health(temp_dir, profile_name="nonexistent_profile")
        assert report["healthy"] is False
        assert any("Profile 'nonexistent_profile' does not exist" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)


def test_doctor_check_health_missing_asset_source() -> None:
    """Tests check_health when an asset's source file is missing from the repository."""
    temp_dir = tempfile.mkdtemp()
    try:
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        # Asset source point to nonexistent_src
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write(
                "version: 2\n"
                "assets:\n"
                "  - id: my_asset\n"
                "    type: copy\n"
                "    source: assets/nonexistent_src\n"
                "    target: /tmp/my_target\n"
                "profiles:\n"
                "  base:\n"
                "    assets: [my_asset]\n"
            )

        report = DoctorService.check_health(temp_dir, profile_name="base")
        assert report["healthy"] is True  # missing source is error level, not critical, so healthy is still True
        assert any("Source file missing for asset 'my_asset'" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)


def test_doctor_check_health_symlink_loop() -> None:
    """Tests check_health when an asset target forms a symlink loop."""
    temp_dir = tempfile.mkdtemp()
    try:
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write(
                "version: 2\n"
                "assets:\n"
                "  - id: my_symlink\n"
                "    type: symlink\n"
                "    source: assets/my_symlink_src\n"
                "    target: /tmp/my_symlink_target\n"
                "profiles:\n"
                "  base:\n"
                "    assets: [my_symlink]\n"
            )

        # Create source
        os.makedirs(os.path.join(temp_dir, "assets"), exist_ok=True)
        with open(os.path.join(temp_dir, "assets", "my_symlink_src"), "w") as f:
            f.write("src")

        # Mock PathHelper.detect_symlink_loop to return True
        with patch("rv.utils.path.PathHelper.detect_symlink_loop", return_value=True):
            report = DoctorService.check_health(temp_dir, profile_name="base")
            assert any("forms a cyclic symlink loop" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)


def test_doctor_check_health_failed_interpolation() -> None:
    """Tests check_health when path interpolation fails (e.g. malformed or missing environment variable)."""
    temp_dir = tempfile.mkdtemp()
    try:
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        # Target contains a missing environment variable to trigger interpolation failure
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write(
                "version: 2\n"
                "assets:\n"
                "  - id: bad_asset\n"
                "    type: copy\n"
                "    source: assets/bad_asset_src\n"
                "    target: /tmp/${MISSING_VAR_DOCTOR}\n"
                "profiles:\n"
                "  base:\n"
                "    assets: [bad_asset]\n"
            )

        os.makedirs(os.path.join(temp_dir, "assets"), exist_ok=True)
        with open(os.path.join(temp_dir, "assets", "bad_asset_src"), "w") as f:
            f.write("bad")

        # Make sure the variable is not in environ
        if "MISSING_VAR_DOCTOR" in os.environ:
            del os.environ["MISSING_VAR_DOCTOR"]

        report = DoctorService.check_health(temp_dir, profile_name="base")
        assert any("Failed path interpolation/verification for asset 'bad_asset'" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)


def test_doctor_check_health_missing_secret_source() -> None:
    """Tests check_health when a secret's source file is missing from the repository."""
    temp_dir = tempfile.mkdtemp()
    try:
        manifest_path = os.path.join(temp_dir, "manifest.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write(
                "version: 2\n"
                "secrets:\n"
                "  - id: my_secret\n"
                "    source: secrets/nonexistent_secret.age\n"
                "    target: /tmp/my_secret_target\n"
                "profiles:\n"
                "  base:\n"
                "    secrets: [my_secret]\n"
            )

        report = DoctorService.check_health(temp_dir, profile_name="base")
        assert any("Source file missing for secret 'my_secret'" in issue["message"] for issue in report["issues"])
    finally:
        shutil.rmtree(temp_dir)
