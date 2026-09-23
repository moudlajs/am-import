// Command am-import creates an Apple Music library playlist from a text file
// of "Artist - Title" lines, using the web player's tokens.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version is overwritten at build time with -ldflags "-X main.version=...".
// It must be a package-level var (not a const) for -X to work.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	fmt.Fprintln(os.Stderr, "am-import: not implemented yet")
	os.Exit(3)
}
