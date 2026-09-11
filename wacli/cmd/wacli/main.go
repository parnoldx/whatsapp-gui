// Command wacli is a WhatsApp CLI for syncing, searching, and sending from
// local scripts. The implementation lives in internal/cli so other binaries
// in this module (e.g. whatsapp-helper) can embed the same commands.
package main

import (
	"os"

	"github.com/openclaw/wacli/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
