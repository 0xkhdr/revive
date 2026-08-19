package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// chunkSize matches the reference's 4 KiB streaming read. Files are streamed rather than read
// whole so a large asset does not have to fit in memory.
const chunkSize = 4096

// SHA256 hashes a file or a directory. A missing path hashes to the empty string, which is what
// the lockfile records for an asset whose source has gone.
func SHA256(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		//nolint:nilerr // A missing source hashes to the empty string, which is what the
		// lockfile records for an asset whose source has gone. It is not a failure to report.
		return "", nil
	}
	h := sha256.New()
	if fi.IsDir() {
		if err := hashDir(h, path, path); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	if err := hashFile(h, path); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashDir walks a directory deterministically: every file in a directory, in name order, before
// descending into its subdirectories, also in name order.
//
// The traversal order is part of the compatibility contract, not a detail. It reproduces
// Python's os.walk with dirs.sort() and sorted(files); filepath.WalkDir's single merged lexical
// order interleaves files and subdirectories differently and would produce a different digest
// for the same tree.
func hashDir(h hash.Hash, root, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	var subdirs []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			subdirs = append(subdirs, path)
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// The relative path goes into the digest before the contents, so a rename is a change
		// even when the bytes are identical.
		_, _ = h.Write([]byte(rel))
		// An unreadable file contributes its path but no content, rather than failing the walk:
		// a checksum is for drift detection, and a permissions problem is doctor's business.
		_ = hashFile(h, path)
	}
	for _, sub := range subdirs {
		if err := hashDir(h, root, sub); err != nil {
			return err
		}
	}
	return nil
}

func hashFile(h hash.Hash, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, chunkSize)
	_, err = io.CopyBuffer(h, f, buf)
	return err
}
