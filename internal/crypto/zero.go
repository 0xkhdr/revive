// Package crypto wraps age encryption, identity and recipient resolution, secure temporary
// files, and memory hygiene for decrypted plaintext.
//
// Plaintext is kept in []byte and never converted to string: Go strings are immutable and
// cannot be zeroed. Callers zero what they own as soon as they are done with it.
package crypto

// Zero overwrites b with zeros. Use it in a defer as soon as plaintext is consumed.
//
// This is the whole of rv's memory hygiene, deliberately: zeroing a slice you still own is the
// correct measure, and anything cleverer is theater.
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
