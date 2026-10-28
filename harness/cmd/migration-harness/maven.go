package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/konveyor/migration-harness/internal/hub"
	"github.com/konveyor/migration-harness/internal/logging"
)

// prepareMavenSettings runs in every invocation, independently of skills or
// workflow stage. An existing user settings file (including a Secret mount)
// takes precedence over Hub's assigned or default Maven credential. Broken
// symlinks fail setup instead of falling back to a Hub credential.
func prepareMavenSettings(client *hub.Client, appID string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	path := filepath.Join(home, ".m2", "settings.xml")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			info, err = os.Stat(path)
			if err != nil {
				return fmt.Errorf("inspect Maven user settings: %w", err)
			}
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Maven user settings path %s is not a regular file", path)
		}
		logging.Info("using existing Maven user settings at %s", path)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Maven user settings: %w", err)
	}

	id, err := hub.ParseAppID(appID)
	if err != nil {
		return fmt.Errorf("invalid APP_ID: %w", err)
	}
	identity, err := client.FetchMavenCreds(id)
	if err != nil {
		return fmt.Errorf("fetch Maven credential: %w", err)
	}
	if identity == nil {
		return nil
	}
	if strings.TrimSpace(identity.Settings) == "" {
		return fmt.Errorf("Maven credential %d has empty settings", identity.ID)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Maven settings directory: %w", err)
	}
	// Exclusive creation also preserves a file or symlink created while the
	// Hub request was in flight. Settings can contain repository passwords.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create Maven user settings: %w", err)
	}
	if err := writeMavenSettings(f, path, identity.Settings); err != nil {
		return err
	}
	logging.Ok("Maven settings prepared at %s", path)
	return nil
}

// writeMavenSettings closes the file and removes incomplete settings on failure.
func writeMavenSettings(f io.WriteCloser, path, settings string) error {
	_, writeErr := io.WriteString(f, settings)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove incomplete Maven user settings: %w", removeErr))
		}
		return fmt.Errorf("write Maven user settings: %w", err)
	}
	return nil
}
