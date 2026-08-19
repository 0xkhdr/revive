// Package transaction owns the filesystem-mutation state machine: atomic writes, the process
// lock, the 7 phases, the journal, and rollback.
package transaction

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// atomicTempPrefix names the sibling temp file an atomic write goes through. The prefix is
// recognizable so a crash leaves an obviously-ours artifact rather than a mystery file.
const atomicTempPrefix = ".rv_atomic_tmp_"

// atomicDirPrefix names the sibling temp directory a directory copy goes through.
const atomicDirPrefix = ".rv_atomic_dir_tmp_"

// AtomicWrite writes content to target through a temp file in the target's own directory,
// fsyncs it, and renames it into place.
//
// The same-directory requirement is not cosmetic: rename is only atomic within one filesystem,
// and a temp file under /tmp routinely lands on a different device. The parent directory is
// fsynced after the rename too, without which the rename itself can be lost on power failure.
func AtomicWrite(target string, content []byte, mode fs.FileMode) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", target, err)
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, atomicTempPrefix)
	if err != nil {
		return fmt.Errorf("creating temp file beside %s: %w", abs, err)
	}
	tmpPath := tmp.Name()
	// Any failure past this point must leave no partial target and no stray temp file.
	defer func() { _ = os.Remove(tmpPath) }()

	if err := writeAndSync(tmp, content, mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", abs, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file for %s: %w", abs, err)
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		return fmt.Errorf("renaming into %s: %w", abs, err)
	}
	return syncDir(dir)
}

func writeAndSync(f *os.File, content []byte, mode fs.FileMode) error {
	if mode != 0 {
		// Set the mode before the content lands, so a secret is never briefly world-readable.
		if err := f.Chmod(mode); err != nil {
			return err
		}
	}
	if _, err := f.Write(content); err != nil {
		return err
	}
	return f.Sync()
}

// syncDir fsyncs a directory so a rename inside it survives a power failure.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s to sync: %w", dir, err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("syncing %s: %w", dir, err)
	}
	return nil
}

// atomicCopyDir replaces target with a recursive copy of source, via a sibling temp directory
// renamed into place. Symlinks inside the tree are preserved as symlinks.
func atomicCopyDir(source, target string) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := removeAny(target); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp(dir, atomicDirPrefix)
	if err != nil {
		return fmt.Errorf("creating temp dir beside %s: %w", target, err)
	}
	if err := copyTree(source, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("copying %s: %w", source, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("renaming into %s: %w", target, err)
	}
	return syncDir(dir)
}

// copyTree copies source into dst, which must already exist, preserving symlinks and modes.
func copyTree(source, dst string) error {
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, out)
		case d.IsDir():
			return os.MkdirAll(out, info.Mode().Perm())
		case d.Type().IsRegular():
			return copyFile(path, out, info.Mode().Perm())
		default:
			// Sockets, devices and fifos are not configuration; skipping beats failing.
			return nil
		}
	})
}

// copyFile copies a regular file, preserving its mode.
func copyFile(source, dst string, mode fs.FileMode) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, content, mode)
}

// removeAny deletes a file, symlink or directory at path. A missing path is not an error.
func removeAny(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", path, err)
	}
	if fi.IsDir() {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("removing %s: %w", path, err)
		}
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}
