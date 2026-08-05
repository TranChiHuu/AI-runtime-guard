package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/airuntimeguard/core/config"
	"github.com/airuntimeguard/core/domain"
	pb "github.com/airuntimeguard/core/gen/runtime/v1"
	"github.com/airuntimeguard/core/server"
	"github.com/airuntimeguard/core/store/sqlite"
)

func cmdStatus() error {
	client, close, err := dial()
	if err != nil {
		return err
	}
	defer close()

	ctx, cancel := withTimeout()
	defer cancel()

	stream, err := client.WatchSession(ctx, &pb.SessionRequest{})
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tAGENT\tSTATE\tRISK\tSIGNALS\tCAPABILITIES")

	// WatchSession replays current state before streaming, so one pass over the
	// initial burst is the whole picture.
	n := 0
	for {
		update, err := stream.Recv()
		if err != nil {
			break
		}
		s := update.GetSession()
		fmt.Fprintf(w, "%s\t%s\t%s\t%d/100\t%d\t%s\n",
			s.GetId(), s.GetAgent(), stateName(s.GetState()),
			s.GetRisk().GetScore(), s.GetSignalCount(), capsOf(s.GetCapabilities()))
		n++
		if n >= 200 {
			break
		}
	}
	w.Flush()

	if n == 0 {
		fmt.Println("no live sessions")
	}
	return nil
}

func cmdDoctor() error {
	fmt.Println("AI Runtime Guard — doctor")
	fmt.Println()

	dir := server.RuntimeDir()
	sock := server.SocketPath()

	check("state directory", dir, dirWritable(dir))
	check("socket path", sock, nil)

	cfg, err := config.Default()
	if err != nil {
		check("risk model", "failed to load", err)
	} else {
		check("risk model", cfg.Version, nil)
	}

	if store, err := sqlite.Open(dir); err != nil {
		check("storage", "unavailable", err)
	} else {
		counts, cerr := store.Counts()
		store.Close()
		if cerr != nil {
			check("storage", "unreadable", cerr)
		} else {
			check("storage", fmt.Sprintf("%d signals, %d decisions, %d sessions, %d preferences",
				counts["signals"], counts["decisions"], counts["sessions"], counts["preferences"]), nil)
		}
	}

	client, closeConn, err := dial()
	if err != nil {
		check("daemon", "not running", err)
		fmt.Println("\nProtection is OFF. Start it with `guard up`.")
		return nil
	}
	defer closeConn()

	ctx, cancel := withTimeout()
	defer cancel()

	h, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		check("daemon", "unhealthy", err)
		return nil
	}

	check("daemon", fmt.Sprintf("v%s, %d live sessions", h.GetVersion(), h.GetLiveSessions()), nil)
	check("api versions", strings.Join(h.GetSupportedApiVersions(), ", "), nil)

	fmt.Println("\nProtection is ON.")
	return nil
}

func cmdReport(sessionID string) error {
	store, err := sqlite.Open(server.RuntimeDir())
	if err != nil {
		return err
	}
	defer store.Close()

	rows, err := store.Decisions(sessionID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no decisions recorded")
		return nil
	}

	for _, r := range rows {
		// Allowed decisions are recorded but not narrated: a report that
		// repeats a rationale for every benign file read is a report nobody
		// reads. A suppressed question is the exception — it landed on ALLOW
		// without anyone agreeing to it, which is exactly what a reader is
		// looking for.
		if r.Action == domain.ActionAllow && !r.Suppressed {
			continue
		}

		label := r.Action.String()
		if r.Suppressed {
			label = r.Action.String() + ", auto-answered"
		}
		fmt.Printf("%s  [%s]  %s\n", r.DecidedAt.Format("15:04:05"), label, r.SessionID)
		fmt.Printf("    what: %s\n", r.Explanation.What)
		fmt.Printf("     why: %s\n", r.Explanation.Why)
		if len(r.Explanation.Evidence) > 0 {
			fmt.Printf("evidence: %s\n", strings.Join(r.Explanation.Evidence, ", "))
		}
		fmt.Printf("guidance: %s\n", r.Explanation.Guidance)
		if r.Resolution != nil {
			fmt.Printf("  answer: %s via %s\n", sourceName(r.Resolution.Source), r.Resolution.Channel)
		}
		fmt.Println()
	}
	return nil
}

func cmdLearned() error {
	fmt.Println("Learned preferences are listed from the daemon's audit engine.")
	fmt.Println("Run `guard report` to see which decision taught each one.")

	store, err := sqlite.Open(server.RuntimeDir())
	if err != nil {
		return err
	}
	defer store.Close()

	counts, err := store.Counts()
	if err != nil {
		return err
	}
	fmt.Printf("\n%d preferences stored\n", counts["preferences"])
	return nil
}

func cmdRevoke(id string) error {
	if id == "" {
		return fmt.Errorf("revoke requires a preference id")
	}

	store, err := sqlite.Open(server.RuntimeDir())
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.DeletePreference(id); err != nil {
		return err
	}
	fmt.Printf("revoked %s\n", id)
	return nil
}

// cmdPurge deletes everything stored locally. The developer must be able to
// remove all of their runtime data with one documented command (Article IX).
func cmdPurge() error {
	store, err := sqlite.Open(server.RuntimeDir())
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Purge(); err != nil {
		return err
	}
	fmt.Printf("purged all runtime data in %s\n", server.RuntimeDir())
	return nil
}

// --- rendering helpers -----------------------------------------------------

func check(label, value string, err error) {
	mark := "ok  "
	if err != nil {
		mark = "FAIL"
	}
	fmt.Printf("  [%s] %-16s %s", mark, label, value)
	if err != nil {
		fmt.Printf("  (%v)", err)
	}
	fmt.Println()
}

func dirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	probe := dir + "/.probe"
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		return err
	}
	return os.Remove(probe)
}

func stateName(s pb.SafetyState) string {
	return domain.SafetyState(s).String()
}

func sourceName(s domain.ResolutionSource) string {
	switch s {
	case domain.ResolutionHuman:
		return "answered by developer"
	case domain.ResolutionTimeout:
		return "timed out"
	case domain.ResolutionHeadless:
		return "no human reachable"
	case domain.ResolutionAdapterFailure:
		return "prompt channel failed"
	case domain.ResolutionDelegated:
		return "handed to the agent's own permission prompt"
	default:
		return "unresolved"
	}
}

func capsOf(c *pb.Capabilities) string {
	if c == nil {
		return "none"
	}
	named := []struct {
		name string
		cap  *pb.Capability
	}{
		{"secret", c.GetSecretAccess()},
		{"fs-read", c.GetFilesystemRead()},
		{"fs-write", c.GetFilesystemWrite()},
		{"shell", c.GetShellExec()},
		{"network", c.GetOutboundNetwork()},
		{"egress", c.GetDataEgress()},
		{"git", c.GetGitWrite()},
		{"untrusted", c.GetUntrustedContext()},
		{"credential", c.GetCredentialMaterial()},
	}

	var out []string
	for _, n := range named {
		if n.cap.GetActive() {
			out = append(out, n.name)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ",")
}
