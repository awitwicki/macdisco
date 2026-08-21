package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var errRefused = errors.New("refusing to remove this path")

// trashDir is a variable so tests can point it at a scratch directory.
var trashDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".Trash")
}

// guard rejects obviously catastrophic targets.
func guard(path string) error {
	clean := filepath.Clean(path)
	if clean == "/" || clean == filepath.Clean(os.Getenv("HOME")) {
		return errRefused
	}
	return nil
}

// moveToTrash asks Finder to delete the path so that "Put Back" works.
// Falls back to renaming into ~/.Trash when Finder is unavailable.
func moveToTrash(path string) error {
	if err := guard(path); err != nil {
		return err
	}
	script := fmt.Sprintf("tell application \"Finder\" to delete (POSIX file %s as alias)", appleScriptString(path))
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err == nil {
		return nil
	} else if msg := strings.TrimSpace(string(out)); msg != "" {
		// keep going, try the fallback, but remember Finder's complaint
		err = fmt.Errorf("finder: %s", msg)
		if fbErr := trashFallback(path); fbErr != nil {
			return fmt.Errorf("%v; fallback: %v", err, fbErr)
		}
		return nil
	}
	return trashFallback(path)
}

// trashFallback renames the path into the trash directory, adding a
// timestamp suffix on name collisions, the way Finder does.
func trashFallback(path string) error {
	dir := trashDir()
	if dir == "" {
		return errors.New("cannot locate Trash")
	}
	base := filepath.Base(path)
	dest := filepath.Join(dir, base)
	if _, err := os.Lstat(dest); err == nil {
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		dest = filepath.Join(dir, fmt.Sprintf("%s %s%s", stem, time.Now().Format("15.04.05"), ext))
	}
	if err := os.Rename(path, dest); err != nil {
		return fmt.Errorf("move to Trash failed (different volume?): %w", err)
	}
	return nil
}

// deletePermanently removes the path and everything beneath it. No undo.
func deletePermanently(path string) error {
	if err := guard(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

// revealInFinder opens a Finder window with the path selected.
func revealInFinder(path string) error {
	return exec.Command("open", "-R", path).Run()
}

// openInFinder opens the directory itself in a Finder window.
func openInFinder(dir string) error {
	return exec.Command("open", dir).Run()
}

// appleScriptString quotes a string for embedding in an AppleScript source.
func appleScriptString(s string) string {
	r := strings.ReplaceAll(s, `\`, `\\`)
	r = strings.ReplaceAll(r, `"`, `\"`)
	return `"` + r + `"`
}
