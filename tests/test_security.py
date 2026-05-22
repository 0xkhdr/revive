"""Test suite for security features including permissions, temp files, scrubbers, and zero buffers."""

import logging
import os
import tempfile
import pytest
from rv.security.permissions import PermissionEnforcer
from rv.security.tempfile import SecureTempFile
from rv.security.scrubber import SecretScrubber, ScrubbingFormatter
from rv.security.zerobuffer import ZeroBuffer
from rv.security.encryptor import AgeEncryptor
from rv.utils.platform import Platform


def test_permission_enforcer() -> None:
    with tempfile.NamedTemporaryFile() as tmp:
        # Enforce 0600 permissions
        PermissionEnforcer.enforce(tmp.name, "0600")
        assert PermissionEnforcer.verify(tmp.name, "0600") is True

        # Enforce 0644 permissions
        PermissionEnforcer.enforce(tmp.name, "0644")
        assert PermissionEnforcer.verify(tmp.name, "0644") is True
        assert PermissionEnforcer.verify(tmp.name, "0600") is False


def test_secure_temp_file() -> None:
    # 1. Test secure file
    path = None
    with SecureTempFile.file() as tmp_path:
        path = tmp_path
        assert os.path.exists(path)
        # Check permissions: must be exactly 0600
        mode = os.stat(path).st_mode & 0o7777
        assert mode == 0o600

        with open(path, "wb") as f:
            f.write(b"super_secret_payload")

    # After context exit, the file must be deleted
    assert os.path.exists(path) is False

    # 2. Test secure directory
    dirpath = None
    with SecureTempFile.directory() as tmp_dir:
        dirpath = tmp_dir
        assert os.path.exists(dirpath)
        mode = os.stat(dirpath).st_mode & 0o7777
        assert mode == 0o700

        # Create nested file
        nested_file = os.path.join(tmp_dir, "nested.txt")
        with open(nested_file, "w") as f:
            f.write("sensitive data")

    # After context exit, directory and nested files must be deleted
    assert os.path.exists(dirpath) is False


def test_secret_scrubber() -> None:
    # 1. Static pattern check (Age private key)
    raw_age_key = "AGE-SECRET-KEY-1qp2x87mvy39r5f2ndj4sk8a4cq6lz7p0w9e8r7t6y5u4i3o2p1qasdfghj"
    scrubbed = SecretScrubber.scrub(f"My key is {raw_age_key}")
    assert "AGE-SECRET-KEY" not in scrubbed
    assert "[REDACTED]" in scrubbed

    # 2. Dynamic secret registration
    SecretScrubber.register_secret("my_super_secret_token_12345")
    scrubbed_dynamic = SecretScrubber.scrub("Access is granted with token my_super_secret_token_12345 to admin.")
    assert "my_super_secret_token_12345" not in scrubbed_dynamic
    assert "[REDACTED]" in scrubbed_dynamic

    # Clean up
    SecretScrubber.clear_dynamic_secrets()

    # 3. Scrubbing Formatter check
    logger = logging.getLogger("test_scrub")
    logger.propagate = False
    logger.setLevel(logging.INFO)

    # Standard string IO stream to capture logs
    import io

    log_capture = io.StringIO()
    handler = logging.StreamHandler(log_capture)
    handler.setFormatter(ScrubbingFormatter("%(message)s"))
    logger.addHandler(handler)

    SecretScrubber.register_secret("my_ultra_secret_db_pass")
    logger.info("Connecting with password: my_ultra_secret_db_pass")

    log_contents = log_capture.getvalue()
    assert "my_ultra_secret_db_pass" not in log_contents
    assert "[REDACTED]" in log_contents

    SecretScrubber.clear_dynamic_secrets()


def test_zero_buffer() -> None:
    buf = bytearray(b"sensitive_password")
    assert buf == b"sensitive_password"
    ZeroBuffer.zero(buf)
    assert buf == b"\x00" * 18


def test_permission_enforcer_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    # Non-existent path raises FileNotFoundError
    with pytest.raises(FileNotFoundError):
        PermissionEnforcer.enforce("/non/existent/path/file.txt", "0600")

    # Invalid permissions format
    with pytest.raises(ValueError):
        PermissionEnforcer.enforce("/etc/passwd", "999")  # Invalid octal

    # Invalid owner name
    with tempfile.NamedTemporaryFile() as tmp:
        with pytest.raises(ValueError) as excinfo:
            PermissionEnforcer.enforce(tmp.name, "0600", owner="non_existent_user_12345")
        assert "does not exist on this system" in str(excinfo.value)

    # os.chmod raising PermissionError
    def mock_chmod(p: str, m: int) -> None:
        raise PermissionError("mocked permission error")

    monkeypatch.setattr(os, "chmod", mock_chmod)
    with tempfile.NamedTemporaryFile() as tmp:
        with pytest.raises(PermissionError) as excinfo:
            PermissionEnforcer.enforce(tmp.name, "0600")
        assert "Failed to change permissions for" in str(excinfo.value)


def test_permission_enforcer_windows(monkeypatch: pytest.MonkeyPatch) -> None:
    """Verifies that PermissionEnforcer handles Windows platform gracefully with warnings."""
    monkeypatch.setattr(PermissionEnforcer, "_is_windows", lambda: True)

    import logging

    logger = logging.getLogger("rv.security.permissions")
    warnings_logged = []
    monkeypatch.setattr(logger, "warning", lambda msg: warnings_logged.append(msg))

    with tempfile.NamedTemporaryFile() as tmp:
        # 1. Enforce read-only POSIX permissions (e.g. 0400)
        PermissionEnforcer.enforce(tmp.name, "0400")
        assert any("Mapped POSIX permissions '0400' to Windows attributes" in w for w in warnings_logged)

        # 2. Enforce writable POSIX permissions (e.g. 0644)
        warnings_logged.clear()
        PermissionEnforcer.enforce(tmp.name, "0644")
        assert any("Mapped POSIX permissions '0644' to Windows attributes" in w for w in warnings_logged)

        # 3. Enforce ownership (chown) - should not raise chown error on Windows
        warnings_logged.clear()
        PermissionEnforcer.enforce(tmp.name, "0644", owner="any_owner")
        assert any("Ownership configuration (chown) is only supported on UNIX/POSIX" in w for w in warnings_logged)

        # 4. Verify on Windows - should return True and log a warning
        warnings_logged.clear()
        assert PermissionEnforcer.verify(tmp.name, "0600") is True
        assert any("Permissions verification bypassed on Windows" in w for w in warnings_logged)


def test_zero_buffer_type_error() -> None:
    with pytest.raises(TypeError):
        ZeroBuffer.zero("string_is_immutable")  # type: ignore


def test_age_encryptor_mocked(monkeypatch: pytest.MonkeyPatch) -> None:
    # Mock Platform.has_tool to return True for 'age'
    monkeypatch.setattr(Platform, "has_tool", lambda name: True)

    import subprocess

    class MockCompletedProcess:
        stdout = "# public key: age1mock\nAGE-SECRET-KEY-1mock"
        stderr = ""
        returncode = 0

    # Mock subprocess.run to verify it gets called with correct args
    called_cmds = []

    def mock_run(cmd, *args, **kwargs):
        called_cmds.append(cmd)
        return MockCompletedProcess()

    monkeypatch.setattr(subprocess, "run", mock_run)

    # 1. Test generate keypair via CLI fallback path by disabling pyrage in mock
    monkeypatch.setattr(AgeEncryptor, "is_pyrage_available", lambda: False)
    pub, priv = AgeEncryptor.generate_keypair()
    assert pub == "age1mock"
    assert priv == "AGE-SECRET-KEY-1mock"

    # 2. Test encrypt file via CLI path
    with tempfile.NamedTemporaryFile() as plain, tempfile.NamedTemporaryFile() as enc:
        AgeEncryptor.encrypt_file(plain.name, enc.name, ["age1recipient1", "age1recipient2"])
        # Check that subprocess.run was called with correct age CLI format
        assert len(called_cmds) > 0
        assert "age" in called_cmds[-1]
        assert "-r" in called_cmds[-1]
        assert "age1recipient1" in called_cmds[-1]

    # 3. Test decrypt file via CLI path
    with (
        tempfile.NamedTemporaryFile() as enc,
        tempfile.NamedTemporaryFile() as plain,
        tempfile.NamedTemporaryFile() as identity,
    ):
        AgeEncryptor.decrypt_file(enc.name, plain.name, identity.name)
        assert "age" in called_cmds[-1]
        assert "-d" in called_cmds[-1]
        assert "-i" in called_cmds[-1]


def test_age_encryptor_keypair() -> None:
    pub, priv = AgeEncryptor.generate_keypair()
    assert pub.startswith("age1")
    assert priv.startswith("AGE-SECRET-KEY-1")


def test_age_encryptor_roundtrip() -> None:
    # Perform a real native pyrage or fallback roundtrip encrypt/decrypt!
    pub, priv = AgeEncryptor.generate_keypair()

    with tempfile.TemporaryDirectory() as tmpdir:
        plain_path = os.path.join(tmpdir, "plain.txt")
        enc_path = os.path.join(tmpdir, "enc.age")
        dec_path = os.path.join(tmpdir, "dec.txt")
        identity_path = os.path.join(tmpdir, "identity.txt")

        # Write plaintext and identity files
        with open(plain_path, "wb") as f:
            f.write(b"this is highly confidential system data")

        with open(identity_path, "w") as f:
            f.write(priv)

        # Encrypt
        AgeEncryptor.encrypt_file(plain_path, enc_path, [pub])
        assert os.path.exists(enc_path)

        # Decrypt
        AgeEncryptor.decrypt_file(enc_path, dec_path, identity_path)
        assert os.path.exists(dec_path)

        with open(dec_path, "rb") as f:
            assert f.read() == b"this is highly confidential system data"


def test_age_encryptor_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    # 1. ValueError on empty recipients
    with pytest.raises(ValueError, match="At least one recipient public key is required"):
        AgeEncryptor.encrypt_file("in.txt", "out.txt", [])

    # 2. FileNotFoundError on missing identity file
    with pytest.raises(FileNotFoundError, match="Age identity file not found"):
        AgeEncryptor.decrypt_file("in.age", "out.txt", "/non/existent/identity")

    # 3. pyrage encrypt exception falling back to age CLI (and age CLI is missing)
    monkeypatch.setattr(AgeEncryptor, "is_pyrage_available", lambda: True)
    import sys

    old_pyrage = sys.modules.get("pyrage")

    class MockPyrage:
        class x25519:
            class Recipient:
                @staticmethod
                def from_str(s: str) -> str:
                    return s

            class Identity:
                @staticmethod
                def generate() -> "MockIdentity":
                    raise RuntimeError("pyrage generate error")

                @staticmethod
                def from_str(s: str) -> str:
                    raise RuntimeError("pyrage identity load error")

        @staticmethod
        def encrypt(data, recipients):
            raise RuntimeError("pyrage encrypt error")

        @staticmethod
        def decrypt(data, identity):
            raise RuntimeError("pyrage decrypt error")

    sys.modules["pyrage"] = MockPyrage  # type: ignore

    monkeypatch.setattr(Platform, "has_tool", lambda name: False)
    with pytest.raises(RuntimeError, match="Pyrage encryption failed and 'age' CLI is not installed"):
        AgeEncryptor.encrypt_file("in.txt", "out.txt", ["age1key"])

    # 4. pyrage decrypt exception falling back to age CLI (and age CLI is missing)
    with tempfile.NamedTemporaryFile() as identity_file:
        with pytest.raises(RuntimeError, match="Pyrage decryption failed and 'age' CLI is not installed"):
            AgeEncryptor.decrypt_file("in.age", "out.txt", identity_file.name)

    # 5. CLI fallback failures (CalledProcessError)
    monkeypatch.setattr(Platform, "has_tool", lambda name: True)
    import subprocess

    def mock_run_error(*args, **kwargs):
        raise subprocess.CalledProcessError(1, "age", stderr="mocked age cli error")

    monkeypatch.setattr(subprocess, "run", mock_run_error)

    with tempfile.NamedTemporaryFile() as plain_file:
        with pytest.raises(RuntimeError, match="Age CLI encryption failed: mocked age cli error"):
            AgeEncryptor.encrypt_file(plain_file.name, "out.txt", ["age1key"])

    with tempfile.NamedTemporaryFile() as enc_file, tempfile.NamedTemporaryFile() as identity_file:
        with pytest.raises(RuntimeError, match="Age CLI decryption failed: mocked age cli error"):
            AgeEncryptor.decrypt_file(enc_file.name, "out.txt", identity_file.name)

    # 6. Generate keypair error paths
    # pyrage error -> age-keygen error
    monkeypatch.setattr(AgeEncryptor, "is_pyrage_available", lambda: False)
    # when age-keygen fails
    with pytest.raises(RuntimeError, match="Failed to generate keypair using age-keygen CLI"):
        AgeEncryptor.generate_keypair()

    # Neither pyrage nor age-keygen available
    monkeypatch.setattr(Platform, "has_tool", lambda name: False)
    with pytest.raises(RuntimeError, match="Neither pyrage nor age-keygen CLI is available"):
        AgeEncryptor.generate_keypair()

    # Clean up sys.modules
    if old_pyrage:
        sys.modules["pyrage"] = old_pyrage
    elif "pyrage" in sys.modules:
        del sys.modules["pyrage"]
