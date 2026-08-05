// Package domain holds the vocabulary of the Runtime Brain: what a signal is,
// what a session is, how risk and decisions are shaped.
//
// It imports nothing outside the standard library. That constraint is the
// structural statement of Article IV — the Brain does not know which AI
// platform it is protecting, and nothing about transport, storage, or
// presentation may leak into the model. See docs/CONSTITUTION.md.
package domain

import "time"

// Phase distinguishes what can still be prevented from what can only be learned
// from. PRE signals are decidable; POST signals update state for future
// decisions and nothing more.
type Phase uint8

const (
	PhaseUnspecified Phase = iota
	PhasePre
	PhasePost
)

// Kind is a closed set. Adding one is a versioned contract change, not an
// adapter's decision (Article XII).
type Kind uint8

const (
	KindUnspecified Kind = iota
	KindPrompt
	KindToolCall
	KindToolResult
	KindFileRead
	KindFileWrite
	KindShellExec
	KindNetwork
	KindGit
	KindMCP
	KindContextIngest
	KindSessionStart
	KindSessionEnd
)

type ActorType uint8

const (
	ActorUnspecified ActorType = iota
	ActorAgent
	ActorUser
	ActorTool
	ActorMCPServer
)

type TargetType uint8

const (
	TargetUnspecified TargetType = iota
	TargetNone
	TargetPath
	TargetCommand
	TargetHost
	TargetRepo
	TargetResource
)

// Scope describes where a target lives. The adapter computes it from
// workspace-local knowledge, because only the adapter can know where the
// workspace root is. It reports where something is — never how bad it is.
type Scope string

const (
	ScopeUnknown  Scope = ""
	ScopeRepo     Scope = "repo"
	ScopeHome     Scope = "home"
	ScopeSystem   Scope = "system"
	ScopeExternal Scope = "external"
)

// SecretShape records that something secret-shaped was touched, without ever
// carrying its value. The Brain reasons about shape and count (Article IX).
type SecretShape string

const (
	SecretNone            SecretShape = ""
	SecretEnvFile         SecretShape = "env-file"
	SecretPrivateKey      SecretShape = "private-key"
	SecretToken           SecretShape = "token"
	SecretCredentialStore SecretShape = "credential-store"
)

type Actor struct {
	Type ActorType
	// Name is a tool or server name. Opaque: engines must not branch on it.
	Name string
}

type Target struct {
	Type  TargetType
	Value string
	Scope Scope
}

// Signal is one normalized observation — the only thing adapters produce.
type Signal struct {
	ID         string
	SessionID  string
	Agent      string // opaque label, carried for reporting only
	Seq        uint64 // monotonic per session; gaps mean signals were lost
	ObservedAt time.Time
	Phase      Phase
	Kind       Kind
	Actor      Actor
	Target     Target

	// Attributes are kind-specific and declared per kind. Secret values must
	// never appear here; use SecretShape and SecretCount.
	Attributes map[string]any

	SecretShape SecretShape
	SecretCount int

	// RawRef is an opaque adapter-local handle used for replay. Optional.
	RawRef string
}

// Attr reads a string attribute. Engines use this rather than indexing the map
// directly so a missing key is never a panic on the decision path.
func (s Signal) Attr(key string) string {
	if s.Attributes == nil {
		return ""
	}
	v, ok := s.Attributes[key].(string)
	if !ok {
		return ""
	}
	return v
}

// TouchesSecret reports whether this signal touched something secret-shaped.
func (s Signal) TouchesSecret() bool { return s.SecretShape != SecretNone }
