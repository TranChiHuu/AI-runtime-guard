package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/airuntimeguard/core/brain"
	"github.com/airuntimeguard/core/domain"
	ctxengine "github.com/airuntimeguard/core/engine/context"
	"github.com/airuntimeguard/core/engine/policy"
	"github.com/airuntimeguard/core/server"
	"github.com/airuntimeguard/core/store/sqlite"
)

// cmdUp starts the daemon and serves until interrupted.
func cmdUp() error {
	dir := server.RuntimeDir()

	store, err := sqlite.Open(dir)
	if err != nil {
		return err
	}
	defer store.Close()

	b := brain.New(brain.Options{
		Sink:      store,
		Workspace: defaultWorkspace(),
		Policies:  defaultPolicies(),
	})
	defer b.Close()

	sock := server.SocketPath()
	ln, err := server.Listen(sock)
	if err != nil {
		return err
	}

	fmt.Printf("AI Runtime Guard %s — risk model %s\n", server.Version, b.ConfigVersion())
	fmt.Printf("listening on %s\n", sock)
	fmt.Printf("state in %s\n", dir)

	// Shut down cleanly so queued audit writes land. Losing the tail of the
	// audit trail on every Ctrl-C would make `guard report` quietly incomplete.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		fmt.Println("\nshutting down")
		ln.Close()
	}()

	if err := server.Serve(ln, server.NewService(b)); err != nil {
		// A closed listener is the normal shutdown path, not a failure.
		if !isClosed(err) {
			return err
		}
	}
	return nil
}

func isClosed(err error) bool {
	return err != nil && (err.Error() == "accept unix: use of closed network connection" ||
		containsClosed(err.Error()))
}

func containsClosed(s string) bool {
	const needle = "use of closed network connection"
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// defaultWorkspace is the baseline until workspace config files land. Package
// registries are allowlisted because an agent fetching dependencies is the
// single most common benign outbound call, and flagging it would train
// developers to dismiss every warning.
func defaultWorkspace() ctxengine.Workspace {
	return ctxengine.Workspace{
		AllowedHosts: []string{
			"registry.npmjs.org",
			"pypi.org",
			"files.pythonhosted.org",
			"proxy.golang.org",
			"crates.io",
			".github.com",
		},
	}
}

// defaultPolicies is the shipped baseline. Deliberately tiny: policy is for
// expressing intent the risk model cannot infer, not for re-encoding the risk
// model as rules.
func defaultPolicies() []policy.Policy {
	return []policy.Policy{
		{
			ID:     "git-is-never-silent",
			Scope:  policy.ScopeGlobal,
			Match:  policy.Matcher{Kinds: []domain.Kind{domain.KindGit}},
			Floor:  domain.ActionNotify,
			Reason: "git operations are never silent",
			Source: "built-in",
		},
	}
}
