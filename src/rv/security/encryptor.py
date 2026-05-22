"""AgeEncryptor to encrypt and decrypt secrets using age / pyrage.

Provides robust fallback to the 'age' system binary if pyrage is unavailable.
"""

import os
import re
import subprocess

from rv.utils.platform import Platform


class AgeEncryptor:
    """Manages encryption and decryption of secrets via pyrage or age CLI."""

    @staticmethod
    def is_pyrage_available() -> bool:
        """Checks if the native pyrage python extension is installed and importable."""
        try:
            import pyrage

            return True
        except ImportError:
            return False

    @classmethod
    def resolve_recipient(cls, recipient: str) -> str:
        """Resolves a recipient public key from a string or a file path."""
        if recipient.startswith("age1"):
            return recipient

        if os.path.exists(recipient) and os.path.isfile(recipient):
            with open(recipient, encoding="utf-8") as f:
                content = f.read().strip()
                for line in content.splitlines():
                    line = line.strip()
                    if line.startswith("age1"):
                        return line
                return content

        return recipient

    @classmethod
    def encrypt_file(cls, in_path: str, out_path: str, recipients: list[str]) -> None:
        """Encrypts a file with the given list of age public key recipients.

        Args:
            in_path: Absolute path to plaintext source file.
            out_path: Absolute path to target encrypted file.
            recipients: List of age public keys (e.g., 'age1...') OR paths to files containing them.
        """
        if not recipients:
            raise ValueError("At least one recipient public key is required for encryption")

        resolved_recipients = [cls.resolve_recipient(r) for r in recipients]

        if cls.is_pyrage_available():
            try:
                import pyrage

                # Read plaintext
                with open(in_path, "rb") as f:
                    plaintext = f.read()

                # Parse recipients
                parsed_recipients = [pyrage.x25519.Recipient.from_str(r) for r in resolved_recipients]

                # Encrypt
                encrypted_data = pyrage.encrypt(plaintext, parsed_recipients)

                # Write to output file
                with open(out_path, "wb") as f:
                    f.write(encrypted_data)
                return
            except Exception as e:
                # If pyrage fails for any reason, fallback to CLI if available
                if not Platform.has_tool("age"):
                    raise RuntimeError(f"Pyrage encryption failed and 'age' CLI is not installed: {e}") from e

        # Fallback to age CLI
        cls._encrypt_file_cli(in_path, out_path, resolved_recipients)

    @classmethod
    def resolve_identity(cls, identity: str) -> str:
        """Resolves an identity from a string or a file path.

        If the string matches the age secret key format or doesn't look like a path,
        it's returned as is. Otherwise, it's treated as a path to a file.
        """
        if identity.startswith("AGE-SECRET-KEY-1"):
            return identity

        # If it looks like a file path, we expect it to exist
        is_path = os.path.sep in identity or identity.startswith(".") or os.path.isabs(identity)

        if is_path:
            if not os.path.exists(identity):
                raise FileNotFoundError(f"Age identity file not found: {identity}")

            if os.path.isfile(identity):
                with open(identity, encoding="utf-8") as f:
                    content = f.read().strip()
                    # If the file contains a key, return it.
                    # It might have comments (like the ones we generate)
                    for line in content.splitlines():
                        line = line.strip()
                        if line.startswith("AGE-SECRET-KEY-1"):
                            return line
                    return content  # Fallback to full content

        return identity

    @classmethod
    def decrypt_file(cls, in_path: str, out_path: str, identity: str) -> None:
        """Decrypts a file using the provided age identity (private key) or identity file path.

        Args:
            in_path: Absolute path to the encrypted source file (.age).
            out_path: Absolute path to target decrypted plaintext file.
            identity: The age identity string OR path to the age identity private key file.
        """
        resolved_identity = cls.resolve_identity(identity)

        if cls.is_pyrage_available():
            try:
                import pyrage

                identity_obj = pyrage.x25519.Identity.from_str(resolved_identity)

                # Read encrypted data
                with open(in_path, "rb") as f:
                    encrypted_data = f.read()

                # Decrypt
                decrypted_data = pyrage.decrypt(encrypted_data, [identity_obj])

                # Write to output file
                with open(out_path, "wb") as f:
                    f.write(decrypted_data)
                return
            except Exception as e:
                if not Platform.has_tool("age"):
                    raise RuntimeError(f"Pyrage decryption failed and 'age' CLI is not installed: {e}") from e

        # Fallback to age CLI
        # For CLI, we might need a temporary file if identity was a string but CLI needs a file
        if not os.path.exists(identity):
            import tempfile
            with tempfile.NamedTemporaryFile(mode="w", delete=False) as tf:
                tf.write(resolved_identity)
                temp_identity_path = tf.name
            try:
                cls._decrypt_file_cli(in_path, out_path, temp_identity_path)
            finally:
                if os.path.exists(temp_identity_path):
                    os.remove(temp_identity_path)
        else:
            cls._decrypt_file_cli(in_path, out_path, identity)

    @classmethod
    def generate_keypair(cls) -> tuple[str, str]:
        """Generates a new age keypair.

        Returns:
            A tuple of (public_key, private_key).
        """
        if cls.is_pyrage_available():
            try:
                import pyrage

                identity = pyrage.x25519.Identity.generate()
                return str(identity.to_public()), str(identity)
            except Exception:
                pass

        # Fallback to age-keygen CLI
        if Platform.has_tool("age-keygen"):
            try:
                result = subprocess.run(["age-keygen"], capture_output=True, text=True, check=True)
                output = result.stdout
                # age-keygen outputs:
                # # public key: age1...
                # AGE-SECRET-KEY-1...
                pub_match = re.search(r"# public key:\s+(age1[a-zA-Z0-9]+)", output)
                priv_match = re.search(r"(AGE-SECRET-KEY-1[a-zA-Z0-9]+)", output)
                if pub_match and priv_match:
                    return pub_match.group(1), priv_match.group(1)
            except Exception as e:
                raise RuntimeError(f"Failed to generate keypair using age-keygen CLI: {e}") from e

        raise RuntimeError("Neither pyrage nor age-keygen CLI is available to generate keypairs")

    @classmethod
    def _encrypt_file_cli(cls, in_path: str, out_path: str, recipients: list[str]) -> None:
        """Encrypts using the age system binary, specifying recipients on the command line."""
        if not Platform.has_tool("age"):
            raise RuntimeError("The 'age' executable is not available in the system PATH")

        cmd = ["age", "-o", out_path]
        for r in recipients:
            cmd.extend(["-r", r])
        cmd.append(in_path)

        try:
            subprocess.run(cmd, check=True, capture_output=True, text=True)
        except subprocess.CalledProcessError as e:
            raise RuntimeError(f"Age CLI encryption failed: {e.stderr}") from e

    @classmethod
    def _decrypt_file_cli(cls, in_path: str, out_path: str, identity_path: str) -> None:
        """Decrypts using the age system binary, utilizing the identity file."""
        if not Platform.has_tool("age"):
            raise RuntimeError("The 'age' executable is not available in the system PATH")

        cmd = ["age", "-d", "-i", identity_path, "-o", out_path, in_path]

        try:
            subprocess.run(cmd, check=True, capture_output=True, text=True)
        except subprocess.CalledProcessError as e:
            raise RuntimeError(f"Age CLI decryption failed: {e.stderr}") from e
