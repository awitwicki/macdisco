package main

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// runReport scans path and prints the largest entries — no UI, nothing is
// ever modified. Useful for scripts, pipes and testing.
func runReport(path string, top int, apparent bool) error {
	s := NewScanner(path)

	done := make(chan struct{})
	go func() { // one-line live progress on stderr
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			case <-tick.C:
				fmt.Fprintf(os.Stderr, "\r  scanning… %s items · %s",
					groupInt(s.FilesSeen.Load()), humanBytes(s.BytesSeen.Load()))
			}
		}
	}()

	start := time.Now()
	root, err := s.Scan(path)
	close(done)
	if err != nil {
		return err
	}

	val := func(n *Node) int64 {
		if apparent {
			return n.Size
		}
		return n.Usage
	}

	children := append([]*Node(nil), root.Children...)
	sort.Slice(children, func(i, j int) bool { return val(children[i]) > val(children[j]) })
	if top > 0 && len(children) > top {
		children = children[:top]
	}

	mode := "disk usage"
	if apparent {
		mode = "apparent size"
	}
	fmt.Printf("%s — %s · %s items · scanned in %.1fs (%s)\n\n",
		path, humanBytes(val(root)), groupInt(root.Items), time.Since(start).Seconds(), mode)
	var maxVal int64
	for _, c := range children {
		if val(c) > maxVal {
			maxVal = val(c)
		}
	}
	for _, c := range children {
		name := c.Name
		if c.IsDir {
			name += "/"
		}
		if c.Denied {
			name += "  [access denied]"
		}
		fmt.Printf("%10s  %s %s  %s\n", humanBytes(val(c)), bar(val(c), maxVal, barWidth), percent(val(c), val(root)), name)
	}
	return nil
}
