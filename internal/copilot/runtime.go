package copilot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const runtimeLockName = ".paperlens-runtime.lock"

// pkgCacheName holds only the CLI's own unpacked, version-pinned package
// (COPILOT_PKG_CACHE_HOME). Sessions, credentials and transcripts stay in
// per-search directories that are deleted after every search.
const pkgCacheName = "pkg-cache"

// Prepare exclusively owns the dedicated runtime root before removing orphan
// state. It also works without a CLI path for keyword-only startup cleanup.
func (a *Agent) Prepare() error {
	if !filepath.IsAbs(a.Root) || (a.Path != "" && !filepath.IsAbs(a.Path)) {
		return errors.New("absolute CLI and dedicated runtime paths required")
	}
	if a.owner != nil {
		return errors.New("runtime root is already owned by this agent")
	}
	if err := os.MkdirAll(a.Root, 0700); err != nil {
		return err
	}
	if err := os.Chmod(a.Root, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(a.Root, runtimeLockName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return errors.New("runtime root is in use by another Paperlens process; choose a different PAPERLENS_RUNTIME_DIR")
	}
	a.owner = lock
	entries, err := os.ReadDir(a.Root)
	if err == nil {
		for _, entry := range entries {
			if entry.Name() == runtimeLockName || (entry.Name() == pkgCacheName && entry.IsDir()) {
				continue
			}
			if !strings.HasPrefix(entry.Name(), "search-") {
				err = errors.New("runtime directory contains non-Paperlens files; choose an empty dedicated directory")
				break
			}
			if removeErr := os.RemoveAll(filepath.Join(a.Root, entry.Name())); removeErr != nil {
				err = errors.New("cannot clean previous Copilot runtime state")
				break
			}
		}
	}
	if err != nil {
		return errors.Join(err, a.Close())
	}
	return nil
}

// Warm unpacks the pinned CLI package into the shared cache once, so searches
// do not each unpack it into their throwaway home.
func (a *Agent) Warm(ctx context.Context) error {
	if a.owner == nil || !filepath.IsAbs(a.Path) {
		return errors.New("prepared runtime and absolute CLI path required")
	}
	dir, err := os.MkdirTemp(a.Root, "search-warm-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, a.Path, "--version")
	cmd.Env = a.env(dir)
	cmd.Dir = dir
	return cmd.Run()
}

// Close releases runtime ownership after all sessions have finished. The lock
// file remains in place so competing processes always lock the same inode.
func (a *Agent) Close() error {
	if a.owner == nil {
		return nil
	}
	file := a.owner
	a.owner = nil
	return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
}
