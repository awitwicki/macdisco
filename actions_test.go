package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuardRefusesDangerousPaths(t *testing.T) {
	if err := guard("/"); err == nil {
		t.Error("guard allowed /")
	}
	home, _ := os.UserHomeDir()
	if err := guard(home); err == nil {
		t.Error("guard allowed home directory")
	}
	if err := guard(t.TempDir()); err != nil {
		t.Errorf("guard refused a temp dir: %v", err)
	}
}

func TestAppleScriptString(t *testing.T) {
	cases := map[string]string{
		`/tmp/plain`:        `"/tmp/plain"`,
		`/tmp/with "quote"`: `"/tmp/with \"quote\""`,
		`/tmp/back\slash`:   `"/tmp/back\\slash"`,
	}
	for in, want := range cases {
		if got := appleScriptString(in); got != want {
			t.Errorf("appleScriptString(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestTrashFallback moves only files created by the test itself, into a fake
// Trash inside the test's temp dir.
func TestTrashFallback(t *testing.T) {
	fakeTrash := t.TempDir()
	oldTrash := trashDir
	trashDir = func() string { return fakeTrash }
	t.Cleanup(func() { trashDir = oldTrash })

	src := t.TempDir()
	victim := filepath.Join(src, "victim.txt")
	writeFile(t, victim, 10)

	if err := trashFallback(victim); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Error("file still exists at original location")
	}
	if _, err := os.Stat(filepath.Join(fakeTrash, "victim.txt")); err != nil {
		t.Error("file not found in trash:", err)
	}

	// name collision gets a suffix instead of overwriting
	writeFile(t, victim, 10)
	if err := trashFallback(victim); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(fakeTrash)
	if len(entries) != 2 {
		t.Errorf("expected 2 files in trash after collision, got %d", len(entries))
	}
}

func TestDeletePermanently(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub")
	writeFile(t, filepath.Join(target, "f"), 10)
	if err := deletePermanently(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("directory still exists after deletePermanently")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:             "0 B",
		512:           "512 B",
		1024:          "1.0 KB",
		1536:          "1.5 KB",
		1048576:       "1.0 MB",
		5368709120:    "5.0 GB",
		1099511627776: "1.0 TB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestGroupInt(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567"}
	for in, want := range cases {
		if got := groupInt(in); got != want {
			t.Errorf("groupInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBar(t *testing.T) {
	if got := bar(0, 100, 4); got != "░░░░" {
		t.Errorf("empty bar = %q", got)
	}
	if got := bar(100, 100, 4); got != "████" {
		t.Errorf("full bar = %q", got)
	}
	if got := bar(1, 1000, 4); got[0] == ' ' {
		t.Errorf("tiny nonzero value renders no bar: %q", got)
	}
}
