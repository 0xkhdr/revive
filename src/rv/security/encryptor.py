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
    def encrypt_file(cls, in_path: str, out_path: str, recipients: list[str]) -> None:
        """Encrypts a file with the given list of age public key recipients.

        Args:
            in_path: Absolute path to plaintext source file.
            out_path: Absolute path to target encrypted file.
            recipients: List of age public keys (e.g., 'age1...').
        """
        if not recipients:
            raise ValueError("At least one recipient public key is required for encryption")

        if cls.is_pyrage_available():
            try:
                import pyrage
                # Read plaintext
                with open(in_path, "rb") as f:
                    plaintext = f.read()

                # Parse recipients
                parsed_recipients = [pyrage.x25519.Recipient.from_str(r) for r in recipients]
                
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
        cls._encrypt_file_cli(in_path, out_path, recipients)

    @classmethod
    def decrypt_file(cls, in_path: str, out_path: str, identity_path: str) -> None:
        """Decrypts a file using the provided age identity (private key) file.

        Args:
            in_path: Absolute path to the encrypted source file (.age).
            out_path: Absolute path to target decrypted plaintext file.
            identity_path: Path to the age identity private key file.
        """
        if not os.path.exists(identity_path):
            raise FileNotFoundError(f"Age identity file not found at: {identity_path}")

        if cls.is_pyrage_available():
            try:
                import pyrage
                # Read private key identity
                with open(identity_path) as f:
                    identity_str = f.read().strip()
                
                identity = pyrage.x25519.Identity.from_str(identity_str)

                # Read encrypted data
                with open(in_path, "rb") as f:
                    encrypted_data = f.read()

                # Decrypt
                decrypted_data = pyrage.decrypt(encrypted_data, [identity])

                # Write to output file
                with open(out_path, "wb") as f:
                    f.write(decrypted_data)
                return
            except Exception as e:
                if not Platform.has_tool("age"):
                    raise RuntimeError(f"Pyrage decryption failed and 'age' CLI is not installed: {e}") from e

        # Fallback to age CLI
        cls._decrypt_file_cli(in_path, out_path, identity_path)

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
                result = subprocess.run(
                    ["age-keygen"],
                    capture_output=True,
                    text=True,
                    check=True
                )
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
