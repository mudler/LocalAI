// SPDX-License-Identifier: MIT

// Command shippedlib serves a local add-on library for manual runs of the
// shipped add-on suite:
//
//	shippedlib -listen 127.0.0.1:0 -addr-file <file> <id>@<version>=<layout> ...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mudler/LocalAI/tests/addons/shipped/lib"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "address to listen on")
	addrFile := flag.String("addr-file", "", "file that receives the library URL once the server listens")
	flag.Parse()
	url, stop, err := lib.Serve(*listen, flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "shippedlib:", err)
		os.Exit(1)
	}
	defer stop()
	if *addrFile != "" {
		if err := os.WriteFile(*addrFile, []byte(url), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "shippedlib:", err)
			os.Exit(1)
		}
	}
	select {}
}
