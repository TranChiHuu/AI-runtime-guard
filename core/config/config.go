// Package config loads the risk model.
//
// Weights, combination bonuses, thresholds and ladder gates are data, not code:
// tuning the model must not require a Go release, and a weight change must be
// reviewable as a diff and validatable by replaying recorded sessions
// (docs/ARCHITECTURE.md §6).
package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/airuntimeguard/core/domain"
)

//go:embed weights.json
var defaultWeights []byte

type Weight struct {
	Points      int    `json:"points"`
	Description string `json:"description"`
}

// Combination is where the intelligence lives. Individual capabilities score
// low; their co-occurrence is the actual threat.
type Combination struct {
	Name        string                  `json:"name"`
	Requires    []domain.CapabilityName `json:"requires"`
	Points      int                     `json:"points"`
	Description string                  `json:"description"`
}

type Confidence struct {
	Base                      float64 `json:"base"`
	PenaltySignalsLost        float64 `json:"penalty_signals_lost"`
	PenaltyUnknownScope       float64 `json:"penalty_unknown_scope"`
	PenaltyUnknownDestination float64 `json:"penalty_unknown_destination"`
	Floor                     float64 `json:"floor"`
}

type Thresholds struct {
	Notify   int `json:"notify"`
	Ask      int `json:"ask"`
	Escalate int `json:"escalate"`

	MinConfidenceAsk      float64 `json:"min_confidence_ask"`
	MinConfidenceEscalate float64 `json:"min_confidence_escalate"`
}

// Irreversible declares what cannot be undone from the developer's machine.
// Only these may reach PAUSE or BLOCK.
type Irreversible struct {
	Kinds   []string `json:"kinds"`
	GitOps  []string `json:"git_ops"`
	FileOps []string `json:"file_ops"`
	kindSet map[domain.Kind]bool
	gitSet  map[string]bool
	fileSet map[string]bool
}

type Interaction struct {
	TimeoutMS               int    `json:"timeout_ms"`
	HeadlessDefaultAsk      string `json:"headless_default_ask"`
	HeadlessDefaultEscalate string `json:"headless_default_escalate"`

	headlessAsk      domain.Action
	headlessEscalate domain.Action
}

type Config struct {
	Version      string                           `json:"version"`
	Capabilities map[domain.CapabilityName]Weight `json:"capabilities"`
	Combinations []Combination                    `json:"combinations"`
	Mitigations  map[string]Weight                `json:"mitigations"`
	Confidence   Confidence                       `json:"confidence"`
	Thresholds   Thresholds                       `json:"thresholds"`
	Irreversible Irreversible                     `json:"irreversible"`
	Interaction  Interaction                      `json:"interaction"`
}

// Default returns the model shipped with the daemon.
func Default() (*Config, error) { return Parse(defaultWeights) }

// MustDefault is for tests and the demo, where a broken embedded config is a
// build error rather than a runtime condition worth handling.
func MustDefault() *Config {
	c, err := Default()
	if err != nil {
		panic(err)
	}
	return c
}

func Parse(data []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.compile(); err != nil {
		return nil, err
	}
	return &c, nil
}

// compile resolves string spellings into typed sets and rejects anything it
// does not recognize. A typo in a config file must fail loudly at load rather
// than silently disabling a detection.
func (c *Config) compile() error {
	if c.Version == "" {
		return fmt.Errorf("config: version is required — audit records must say which model decided")
	}

	for name := range c.Capabilities {
		if !known(name) {
			return fmt.Errorf("config: unknown capability %q", name)
		}
	}
	for _, comb := range c.Combinations {
		if len(comb.Requires) < 2 {
			return fmt.Errorf("config: combination %q needs at least two capabilities", comb.Name)
		}
		for _, r := range comb.Requires {
			if !known(r) {
				return fmt.Errorf("config: combination %q requires unknown capability %q", comb.Name, r)
			}
		}
	}

	c.Irreversible.kindSet = map[domain.Kind]bool{}
	for _, k := range c.Irreversible.Kinds {
		kind, ok := domain.ParseKind(k)
		if !ok {
			return fmt.Errorf("config: unknown irreversible kind %q", k)
		}
		c.Irreversible.kindSet[kind] = true
	}
	c.Irreversible.gitSet = set(c.Irreversible.GitOps)
	c.Irreversible.fileSet = set(c.Irreversible.FileOps)

	var err error
	if c.Interaction.headlessAsk, err = parseAction(c.Interaction.HeadlessDefaultAsk); err != nil {
		return err
	}
	if c.Interaction.headlessEscalate, err = parseAction(c.Interaction.HeadlessDefaultEscalate); err != nil {
		return err
	}
	return nil
}

// IsIrreversible reports whether a signal describes something that cannot be
// undone from this machine.
func (c *Config) IsIrreversible(sig domain.Signal) bool {
	if c.Irreversible.kindSet[sig.Kind] {
		return true
	}
	switch sig.Kind {
	case domain.KindGit:
		return c.Irreversible.gitSet[sig.Attr("git_op")]
	case domain.KindFileWrite:
		return c.Irreversible.fileSet[sig.Attr("file_op")]
	}
	return false
}

// HeadlessDefault is what to do when no human is reachable. It is a judgment
// about what is safe when nobody is watching, so it lives in the Brain's config
// and the adapter merely applies it.
func (c *Config) HeadlessDefault(escalated bool) domain.Action {
	if escalated {
		return c.Interaction.headlessEscalate
	}
	return c.Interaction.headlessAsk
}

func (c *Config) Timeout() time.Duration {
	return time.Duration(c.Interaction.TimeoutMS) * time.Millisecond
}

func known(name domain.CapabilityName) bool {
	for _, n := range domain.AllCapabilities {
		if n == name {
			return true
		}
	}
	return false
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

func parseAction(s string) (domain.Action, error) {
	switch s {
	case "ALLOW":
		return domain.ActionAllow, nil
	case "NOTIFY":
		return domain.ActionNotify, nil
	case "ASK":
		return domain.ActionAsk, nil
	case "PAUSE":
		return domain.ActionPause, nil
	case "BLOCK":
		return domain.ActionBlock, nil
	}
	return domain.ActionUnspecified, fmt.Errorf("config: unknown action %q", s)
}
