package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
)

// Node is one entry in the scanned tree. Sizes for directories are filled
// in by aggregate() after the parallel walk completes.
type Node struct {
	Name     string
	Parent   *Node
	IsDir    bool
	Denied   bool  // could not read (permissions, I/O error)
	Size     int64 // apparent size in bytes
	Usage    int64 // allocated size on disk in bytes
	Items    int64 // total number of entries beneath a directory
	Children []*Node
}

// Path returns the absolute path of the node.
func (n *Node) Path() string {
	if n.Parent == nil {
		return n.Name
	}
	return filepath.Join(n.Parent.Path(), n.Name)
}

type devino struct {
	dev uint64
	ino uint64
}

// Scanner walks a directory tree with a bounded pool of goroutines.
type Scanner struct {
	sem       chan struct{}
	wg        sync.WaitGroup
	hardlinks sync.Map // devino -> struct{}, files with nlink > 1 counted once
	excludes  map[string]bool

	// live progress counters, safe to read while scanning
	FilesSeen atomic.Int64
	BytesSeen atomic.Int64
}

// Paths never descended into when scanning "/" on macOS: the data volume is
// already reachable through firmlinks (/Users, /Applications, ...) so
// descending into /System/Volumes/Data would double-count everything, and
// /Volumes holds other disks entirely.
var rootExcludes = []string{
	"/System/Volumes/Data",
	"/System/Volumes/Preboot",
	"/System/Volumes/VM",
	"/System/Volumes/Update",
	"/System/Volumes/Hardware",
	"/System/Volumes/iSCPreboot",
	"/System/Volumes/xarts",
	"/Volumes",
	"/dev",
}

// workerCount decides how many directories are walked concurrently. The scan
// is dominated by lstat syscall latency, not CPU, so it pays to run well past
// the core count. Overridable for benchmarking via MACDISCO_WORKERS.
func workerCount() int {
	if v, err := strconv.Atoi(os.Getenv("MACDISCO_WORKERS")); err == nil && v > 0 {
		return v
	}
	return runtime.NumCPU() * 6
}

func NewScanner(root string) *Scanner {
	s := &Scanner{
		sem:      make(chan struct{}, workerCount()),
		excludes: map[string]bool{},
	}
	if root == "/" {
		for _, p := range rootExcludes {
			s.excludes[p] = true
		}
	}
	return s
}

// Scan walks root and returns the aggregated tree. Blocking; progress can be
// polled from another goroutine via FilesSeen/BytesSeen.
func (s *Scanner) Scan(root string) (*Node, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	top := &Node{Name: root, IsDir: true}
	s.walk(root, top)
	s.wg.Wait()
	aggregate(top)
	return top, nil
}

func (s *Scanner) walk(path string, node *Node) {
	if !bulkDisabled && s.walkBulk(path, node) {
		return
	}
	s.walkStat(path, node)
}

// join builds a child path without doubling the separator when the parent is
// the filesystem root — exclude matching relies on canonical paths.
func join(dir, name string) string {
	if dir == "/" {
		return dir + name
	}
	return dir + string(filepath.Separator) + name
}

// descend walks a subdirectory, in a new goroutine when a slot is free.
func (s *Scanner) descend(path string, node *Node) {
	select {
	case s.sem <- struct{}{}:
		s.wg.Add(1)
		go func() {
			defer func() { <-s.sem; s.wg.Done() }()
			s.walk(path, node)
		}()
	default:
		s.walk(path, node)
	}
}

// countFile applies hardlink dedup and updates the live counters.
func (s *Scanner) countFile(child *Node, size, usage int64, nlink uint32, dev, ino uint64) {
	if nlink > 1 {
		if _, seen := s.hardlinks.LoadOrStore(devino{dev, ino}, struct{}{}); seen {
			size, usage = 0, 0
		}
	}
	child.Size = size
	child.Usage = usage
	s.FilesSeen.Add(1)
	s.BytesSeen.Add(usage)
}

// walkBulk lists path via getattrlistbulk. Returns false when the
// filesystem does not support it, so walk falls back to walkStat.
func (s *Scanner) walkBulk(path string, node *Node) bool {
	entries, ok, err := readDirBulk(path)
	if !ok {
		return false
	}
	if err != nil {
		node.Denied = true
		return true
	}
	children := make([]*Node, 0, len(entries))
	for _, e := range entries {
		full := join(path, e.name)
		if s.excludes[full] {
			continue
		}
		child := &Node{Name: e.name, Parent: node}
		children = append(children, child)
		if e.isDir {
			child.IsDir = true
			s.descend(full, child)
			continue
		}
		s.countFile(child, e.size, e.alloc, e.nlink, uint64(e.dev), e.ino)
	}
	node.Children = children
	return true
}

// walkStat is the portable readdir + lstat path, used when bulk listing is
// unsupported (network filesystems) or disabled via MACDISCO_NO_BULK.
func (s *Scanner) walkStat(path string, node *Node) {
	entries, err := os.ReadDir(path)
	if err != nil {
		node.Denied = true
		return
	}
	children := make([]*Node, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		full := join(path, name)
		if s.excludes[full] {
			continue
		}
		child := &Node{Name: name, Parent: node}
		children = append(children, child)
		if entry.IsDir() {
			child.IsDir = true
			s.descend(full, child)
			continue
		}
		info, err := entry.Info()
		if err != nil {
			child.Denied = true
			continue
		}
		size := info.Size()
		usage := size
		var nlink uint32
		var dev, ino uint64
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			usage = st.Blocks * 512
			nlink = uint32(st.Nlink)
			dev, ino = uint64(st.Dev), st.Ino
		}
		s.countFile(child, size, usage, nlink, dev, ino)
	}
	node.Children = children
}

// aggregate fills in directory totals bottom-up.
func aggregate(n *Node) {
	if !n.IsDir {
		return
	}
	var size, usage, items int64
	for _, c := range n.Children {
		aggregate(c)
		size += c.Size
		usage += c.Usage
		items += 1 + c.Items
	}
	n.Size, n.Usage, n.Items = size, usage, items
}

// detach removes a node from its parent and subtracts its totals from every
// ancestor. Used after a successful delete or move to Trash.
func detach(n *Node) {
	p := n.Parent
	if p == nil {
		return
	}
	for i, c := range p.Children {
		if c == n {
			p.Children = append(p.Children[:i], p.Children[i+1:]...)
			break
		}
	}
	dItems := n.Items + 1
	for a := p; a != nil; a = a.Parent {
		a.Size -= n.Size
		a.Usage -= n.Usage
		a.Items -= dItems
	}
	n.Parent = nil
}
