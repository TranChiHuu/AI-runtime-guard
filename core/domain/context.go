package domain

// Trust is how much a workspace or destination is believed to be benign.
type Trust uint8

const (
	TrustUnknown Trust = iota
	TrustTrusted
	TrustUntrusted
)

// Sensitivity of the resources a session has touched.
type Sensitivity uint8

const (
	SensitivityLow Sensitivity = iota
	SensitivityMedium
	SensitivityHigh
)

// Context is the interpretation layer between raw session state and risk: what
// is this session doing, and in what surroundings.
//
// It is derived, never stored as truth — recomputing it from session state is
// what lets a replay produce identical results (docs/RUNTIME_MODEL.md §11).
type Context struct {
	WorkspaceTrust   Trust
	Sensitivity      Sensitivity
	DestinationTrust Trust
	// Destination is the host the current signal targets, if any. Carried so
	// explanations can name it without re-parsing the signal.
	Destination string
	// Supervised is false when the developer has turned off prompting for this
	// session. An unattended session can still be protected — it just cannot be
	// asked.
	Supervised bool

	// SignalsLost marks a session with sequence gaps. Its risk is understated,
	// so confidence must drop rather than the score silently being trusted.
	SignalsLost bool
}
