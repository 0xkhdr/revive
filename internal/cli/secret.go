package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/transaction"
)

func newSecretCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{Use: "secret", Short: "Manage age keys and encrypted files"}
	cmd.AddCommand(
		newKeygenCommand(env),
		newEncryptCommand(env),
		newDecryptCommand(env),
		newRotateCommand(env),
	)
	return cmd
}

func newKeygenCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an age keypair",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, _ := cmd.Flags().GetString("output")
			pub, identity, err := crypto.GenerateKeypair()
			if err != nil {
				return err
			}

			if out == "" {
				// Without --output the private key is not stored anywhere, and the user has to
				// be told plainly rather than discovering it later.
				e := env
				e.line("public key:  %s", pub)
				e.line("private key: %s", identity)
				e.line("")
				e.line("WARNING: this private key was NOT saved. Store it now, or re-run with " +
					"--output to write it to a file.")
				return nil
			}
			if err := crypto.WriteIdentityFile(out, pub, identity); err != nil {
				return err
			}
			env.line("wrote %s (mode 0600)", out)
			env.line("public key: %s", pub)
			return nil
		},
	}
	cmd.Flags().StringP("output", "o", "", "Write the identity file here, mode 0600")
	return cmd
}

func newEncryptCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encrypt <file>",
		Short: "Encrypt a file to one or more age recipients",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, _ := cmd.Flags().GetString("output")
			recipients, _ := cmd.Flags().GetStringArray("recipient")
			if out == "" {
				return fmt.Errorf("%w: --output is required", ErrUsage)
			}
			if len(recipients) == 0 {
				return fmt.Errorf("%w: at least one --recipient is required", ErrUsage)
			}

			plaintext, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("reading %s: %w", args[0], err)
			}
			defer crypto.Zero(plaintext)

			ciphertext, err := crypto.Encrypt(plaintext, recipients)
			if err != nil {
				return err
			}
			if err := transaction.AtomicWrite(out, ciphertext, 0o644); err != nil {
				return err
			}
			env.line("encrypted %s to %s for %d recipient(s)", args[0], out, len(recipients))
			return nil
		},
	}
	cmd.Flags().StringP("output", "o", "", "Encrypted output path")
	cmd.Flags().StringArrayP("recipient", "r", nil, "age public key or a file containing one; repeatable")
	return cmd
}

func newDecryptCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decrypt <file>",
		Short: "Decrypt a file with an identity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, _ := cmd.Flags().GetString("output")
			identity, _ := cmd.Flags().GetString("identity")
			if out == "" {
				return fmt.Errorf("%w: --output is required", ErrUsage)
			}

			identity, err := env.resolveIdentity(identity)
			if err != nil {
				return err
			}
			ciphertext, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("reading %s: %w", args[0], err)
			}
			plaintext, err := crypto.Decrypt(ciphertext, identity)
			if err != nil {
				return err
			}
			defer crypto.Zero(plaintext)

			// Decrypted content never lands with a permissive mode, whatever the umask says.
			if err := transaction.AtomicWrite(out, plaintext, 0o600); err != nil {
				return err
			}
			env.line("decrypted %s to %s (mode 0600)", args[0], out)
			return nil
		},
	}
	cmd.Flags().StringP("output", "o", "", "Plaintext output path")
	addIdentityFlag(cmd)
	return cmd
}

func newRotateCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate <file>",
		Short: "Re-encrypt a secret to new recipients",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			recipients, _ := cmd.Flags().GetStringArray("new-recipient")
			fromPlaintext, _ := cmd.Flags().GetString("from-plaintext")
			confirmed, _ := cmd.Flags().GetBool("confirm")
			identity, _ := cmd.Flags().GetString("identity")

			if len(recipients) == 0 {
				return fmt.Errorf("%w: at least one --new-recipient is required", ErrUsage)
			}

			var plaintext []byte
			if fromPlaintext != "" {
				// This path destroys the source file, so it may not be reachable by accident.
				if !confirmed {
					return fmt.Errorf("%w: --from-plaintext securely wipes and deletes %s, "+
						"so it requires --confirm", ErrUsage, fromPlaintext)
				}
				raw, err := os.ReadFile(fromPlaintext)
				if err != nil {
					return fmt.Errorf("reading %s: %w", fromPlaintext, err)
				}
				plaintext = raw
			} else {
				resolved, err := env.resolveIdentity(identity)
				if err != nil {
					return err
				}
				ciphertext, err := os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("reading %s: %w", args[0], err)
				}
				plaintext, err = crypto.Decrypt(ciphertext, resolved)
				if err != nil {
					return err
				}
			}
			defer crypto.Zero(plaintext)

			ciphertext, err := crypto.Encrypt(plaintext, recipients)
			if err != nil {
				return err
			}
			if err := transaction.AtomicWrite(args[0], ciphertext, 0o644); err != nil {
				return err
			}
			env.line("rotated %s to %d recipient(s)", args[0], len(recipients))

			if fromPlaintext != "" {
				if err := wipe(fromPlaintext); err != nil {
					return fmt.Errorf("wiping %s: %w", fromPlaintext, err)
				}
				env.line("securely wiped and deleted %s", fromPlaintext)
			}
			return nil
		},
	}
	addIdentityFlag(cmd)
	cmd.Flags().StringArray("new-recipient", nil, "New age recipient; repeatable")
	cmd.Flags().String("from-plaintext", "", "Encrypt this plaintext directly, then wipe and delete it")
	cmd.Flags().Bool("confirm", false, "Required with --from-plaintext")
	return cmd
}

// wipe overwrites a file with zeros, syncs, and unlinks it.
func wipe(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(make([]byte, fi.Size())); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}

// resolveIdentity applies the documented order: --identity if given (and it MUST exist), then
// the three default locations.
func (e *Env) resolveIdentity(flag string) (string, error) {
	if flag != "" {
		if _, err := os.Stat(flag); err != nil {
			return "", fmt.Errorf("identity file %s: %w", flag, err)
		}
		return flag, nil
	}
	for _, candidate := range e.Paths.IdentityCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: no identity found; create %s or pass --identity",
		crypto.ErrIdentityRequired, e.Paths.IdentityFile)
}
