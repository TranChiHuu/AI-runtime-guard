// Command guard is the primary interface to AI Runtime Guard.
//
// It is a gRPC client with no privileged path into the Brain: every capability
// it exposes is reachable through the same Runtime API the adapters and the
// dashboard use (Article X).
package main

import (
	"fmt"
	"os"
)

const usage = `guard — AI Runtime Guard

  guard up               start the runtime daemon
  guard status           live sessions, states, risk
  guard doctor           verify socket, config, storage
  guard report [session] decisions and their explanations
  guard replay <session> re-run a recorded session against the current model
  guard learned          list learned preferences
  guard revoke <id>      remove a learned preference
  guard purge            delete all locally stored runtime data
  guard simulate         run built-in scenarios through the daemon

Environment:
  GUARD_HOME    state directory (default: ~/.local/state/ai-runtime-guard)
  GUARD_SOCKET  socket path (default: $GUARD_HOME/guard.sock)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	args := os.Args[2:]
	var err error

	switch os.Args[1] {
	case "up":
		err = cmdUp()
	case "status":
		err = cmdStatus()
	case "doctor":
		err = cmdDoctor()
	case "report":
		err = cmdReport(arg(args, 0))
	case "replay":
		err = cmdReplay(arg(args, 0))
	case "learned":
		err = cmdLearned()
	case "revoke":
		err = cmdRevoke(arg(args, 0))
	case "purge":
		err = cmdPurge()
	case "simulate":
		err = cmdSimulate()
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "guard: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "guard:", err)
		os.Exit(1)
	}
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}
