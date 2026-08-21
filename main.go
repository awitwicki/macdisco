package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/term"
)

// version is the single source of truth for releases: when a push to main
// carries a new value here, CI tags v<version> and publishes binaries.
var version = "1.0.1"

func main() {
	var (
		report      bool
		top         int
		apparent    bool
		showVersion bool
	)
	flag.BoolVar(&report, "report", false, "scan and print a report instead of the interactive UI")
	flag.BoolVar(&report, "r", false, "shorthand for -report")
	flag.IntVar(&top, "n", 25, "number of entries shown in report mode")
	flag.BoolVar(&apparent, "a", false, "use apparent sizes instead of disk usage")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `macdisco %s — fast disk usage explorer for macOS

usage: macdisco [options] [path]

Scans path (default: current directory) and opens an interactive explorer.
Use "macdisco /" to analyze the whole disk (grant your terminal Full Disk
Access in System Settings for complete results).

options:
`, version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if showVersion {
		fmt.Println("macdisco", version)
		return
	}

	path := "."
	if flag.NArg() > 0 {
		path = flag.Arg(0)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fail(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		fail(err)
	}
	if !info.IsDir() {
		fail(fmt.Errorf("%s is not a directory", abs))
	}

	if !report && !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "macdisco: stdout is not a terminal, falling back to -report")
		report = true
	}

	if report {
		if err := runReport(abs, top, apparent); err != nil {
			fail(err)
		}
		return
	}
	if err := NewUI(abs).Run(); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "macdisco:", err)
	os.Exit(1)
}
