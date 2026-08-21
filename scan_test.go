package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildTree creates:
//
//	root/a.txt        100 B
//	root/sub/b.txt    200 B
//	root/sub/deep/c   300 B
//	root/empty/
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), 100)
	writeFile(t, filepath.Join(root, "sub", "b.txt"), 200)
	writeFile(t, filepath.Join(root, "sub", "deep", "c"), 300)
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func scanTree(t *testing.T, root string) *Node {
	t.Helper()
	n, err := NewScanner(root).Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func child(t *testing.T, n *Node, name string) *Node {
	t.Helper()
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("child %q not found in %s", name, n.Path())
	return nil
}

func TestScanAggregatesSizes(t *testing.T) {
	root := buildTree(t)
	n := scanTree(t, root)

	if n.Size != 600 {
		t.Errorf("root apparent size = %d, want 600", n.Size)
	}
	if n.Items != 6 { // a.txt, sub, sub/b.txt, sub/deep, sub/deep/c, empty
		t.Errorf("root items = %d, want 6", n.Items)
	}
	sub := child(t, n, "sub")
	if sub.Size != 500 || sub.Items != 3 {
		t.Errorf("sub size/items = %d/%d, want 500/3", sub.Size, sub.Items)
	}
	if empty := child(t, n, "empty"); empty.Size != 0 || empty.Items != 0 {
		t.Errorf("empty dir has size %d items %d", empty.Size, empty.Items)
	}
	if n.Usage < n.Size {
		t.Errorf("disk usage %d smaller than apparent %d on regular files", n.Usage, n.Size)
	}
}

func TestScanPathsAndParents(t *testing.T) {
	root := buildTree(t)
	n := scanTree(t, root)
	deep := child(t, child(t, n, "sub"), "deep")
	want := filepath.Join(root, "sub", "deep")
	if deep.Path() != want {
		t.Errorf("Path() = %q, want %q", deep.Path(), want)
	}
}

func TestHardlinksCountedOnce(t *testing.T) {
	root := t.TempDir()
	orig := filepath.Join(root, "orig")
	writeFile(t, orig, 1000)
	if err := os.Link(orig, filepath.Join(root, "link")); err != nil {
		t.Skip("hardlinks unsupported:", err)
	}
	n := scanTree(t, root)
	if n.Size != 1000 {
		t.Errorf("hardlinked file double counted: size = %d, want 1000", n.Size)
	}
}

func TestSymlinksNotFollowed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real", "big"), 5000)
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	n := scanTree(t, root)
	// The symlink itself is a few bytes; following it would add 5000 more.
	if n.Size >= 10000 {
		t.Errorf("symlink target counted twice: size = %d", n.Size)
	}
	if child(t, n, "alias").IsDir {
		t.Error("symlink to dir treated as dir")
	}
}

func TestDeniedDirectoryMarked(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, permissions not enforced")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	writeFile(t, filepath.Join(locked, "secret"), 100)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	n := scanTree(t, root)
	if !child(t, n, "locked").Denied {
		t.Error("unreadable dir not marked Denied")
	}
}

func TestJoinRootPaths(t *testing.T) {
	if got := join("/", "System"); got != "/System" {
		t.Errorf(`join("/", "System") = %q`, got)
	}
	if got := join("/a", "b"); got != "/a/b" {
		t.Errorf(`join("/a", "b") = %q`, got)
	}
}

func TestExcludesSkipped(t *testing.T) {
	root := buildTree(t)
	s := NewScanner(root)
	s.excludes[filepath.Join(root, "sub")] = true
	n, err := s.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if n.Size != 100 { // only a.txt remains
		t.Errorf("size with sub/ excluded = %d, want 100", n.Size)
	}
	for _, c := range n.Children {
		if c.Name == "sub" {
			t.Error("excluded dir still present")
		}
	}
}

// TestBulkMatchesStat verifies the getattrlistbulk fast path returns the
// same tree as the portable readdir+lstat path.
func TestBulkMatchesStat(t *testing.T) {
	root := buildTree(t)
	writeFile(t, filepath.Join(root, "sub", "deep", "más 日本 файл.bin"), 4096)

	fast := scanTree(t, root)

	bulkDisabled = true
	t.Cleanup(func() { bulkDisabled = false })
	slow := scanTree(t, root)

	var compare func(a, b *Node)
	compare = func(a, b *Node) {
		if a.Name != b.Name || a.IsDir != b.IsDir || a.Size != b.Size ||
			a.Usage != b.Usage || a.Items != b.Items || len(a.Children) != len(b.Children) {
			t.Fatalf("bulk/stat mismatch at %s: %+v vs %+v", a.Path(), a, b)
		}
		bByName := map[string]*Node{}
		for _, c := range b.Children {
			bByName[c.Name] = c
		}
		for _, c := range a.Children {
			other, ok := bByName[c.Name]
			if !ok {
				t.Fatalf("entry %s missing from stat scan", c.Path())
			}
			compare(c, other)
		}
	}
	compare(fast, slow)
}

func TestDetachUpdatesAncestors(t *testing.T) {
	root := buildTree(t)
	n := scanTree(t, root)
	sub := child(t, n, "sub")
	deep := child(t, sub, "deep")

	detach(deep) // removes 300 B and 2 items (deep + c)

	if n.Size != 300 {
		t.Errorf("root size after detach = %d, want 300", n.Size)
	}
	if n.Items != 4 {
		t.Errorf("root items after detach = %d, want 4", n.Items)
	}
	if sub.Size != 200 || sub.Items != 1 {
		t.Errorf("sub after detach = %d/%d, want 200/1", sub.Size, sub.Items)
	}
	for _, c := range sub.Children {
		if c == deep {
			t.Error("detached node still present in parent")
		}
	}
}
