// Command gofer sends files directly between devices, peer to peer.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NZ-Linix/gofer/internal/update"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `gofer — send files directly to another device, peer to peer.

Usage:
  gofer login <username>          set your username on this device
  gofer whoami                    show your username and peer ID
  gofer receive [flags]           stay online and receive files
      --dir <path>                  where to save files (default ~/Downloads)
      --yes                         accept every incoming file without asking
      --port <n>                    fixed TCP/UDP port (helps with port forwarding)
      -v                            print network addresses
  gofer send <file> <username>    send a file to someone running 'gofer receive'
  gofer contacts                  list pinned contacts
  gofer contacts add <name> <id>  pin a username to a peer ID
  gofer contacts rm <name>        remove a contact
  gofer update                    update gofer to the latest release
  gofer version                   show the installed version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	update.CleanupOldBinary()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "login":
		err = cmdLogin(args)
	case "whoami":
		err = cmdWhoami()
	case "receive", "recv":
		err = cmdReceive(ctx, args)
	case "send":
		err = cmdSend(ctx, args)
	case "contacts":
		err = cmdContacts(args)
	case "update", "upgrade":
		err = update.Run(version)
	case "version", "--version", "-v":
		fmt.Println("gofer", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}
}
