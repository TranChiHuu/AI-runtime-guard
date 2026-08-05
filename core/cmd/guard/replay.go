package main

import (
	"fmt"
	"time"

	"github.com/airuntimeguard/core/brain"
	"github.com/airuntimeguard/core/domain"
	"github.com/airuntimeguard/core/server"
	"github.com/airuntimeguard/core/store/sqlite"
)

// cmdReplay re-runs a recorded session against the current risk model and
// reports where the outcome changed.
//
// This is not a convenience command. It is how any change to weights,
// thresholds, or engine logic gets validated before release: given the same
// signals, policies, and preferences, the Brain must produce identical
// decisions (docs/RUNTIME_MODEL.md §11). A diff here is either a bug or an
// intentional model change that someone has to sign off on.
func cmdReplay(sessionID string) error {
	store, err := sqlite.Open(server.RuntimeDir())
	if err != nil {
		return err
	}
	defer store.Close()

	if sessionID == "" {
		return listSessions(store)
	}

	signals, err := store.Signals(sessionID)
	if err != nil {
		return err
	}
	if len(signals) == 0 {
		return fmt.Errorf("no signals recorded for session %q", sessionID)
	}

	recorded, err := store.Decisions(sessionID)
	if err != nil {
		return err
	}
	before := make(map[string]domain.Action, len(recorded))
	for _, r := range recorded {
		before[r.SignalID] = r.Action
	}

	// A replay runs with no sink: it must never write over the history it is
	// checking itself against. The clock is fixed so timestamps cannot differ
	// for reasons unrelated to the model.
	fixed := time.Unix(0, 0).UTC()
	b := brain.New(brain.Options{
		Workspace: defaultWorkspace(),
		Policies:  defaultPolicies(),
		Clock:     func() time.Time { return fixed },
	})
	defer b.Close()

	fmt.Printf("replaying %s — %d signals against risk model %s\n\n",
		sessionID, len(signals), b.ConfigVersion())

	changed, missing := 0, 0
	for _, sig := range signals {
		d, err := b.Decide(sig)
		if err != nil {
			return err
		}

		prev, ok := before[sig.ID]
		switch {
		case !ok:
			missing++
			fmt.Printf("  %-12s %-8s  (no recorded decision)\n", sig.ID, d.Action)
		case prev != d.Action:
			changed++
			fmt.Printf("  %-12s %-8s -> %-8s  CHANGED\n", sig.ID, prev, d.Action)
			fmt.Printf("               %s\n", d.Explanation.Why)
		default:
			fmt.Printf("  %-12s %-8s  unchanged\n", sig.ID, d.Action)
		}
	}

	fmt.Println()
	switch {
	case changed == 0 && missing == 0:
		fmt.Printf("replay identical — all %d decisions reproduced exactly\n", len(signals))
	case changed == 0:
		fmt.Printf("no decision changed (%d signals had no recorded decision)\n", missing)
	default:
		fmt.Printf("%d of %d decisions changed under the current model\n", changed, len(signals))
	}
	return nil
}

func listSessions(store *sqlite.Store) error {
	ids, err := store.SessionIDs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("no recorded sessions to replay")
	}
	fmt.Println("recorded sessions:")
	for _, id := range ids {
		fmt.Printf("  %s\n", id)
	}
	return fmt.Errorf("specify one: guard replay <session>")
}
