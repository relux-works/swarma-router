// Package recommend produces inert model recommendations from explicit owner
// policy and dated evidence. It has no execution, discovery or persistence ports.
package recommend

const Contract = "router-recommend/1"

// RawInputs preserves the caller's original bytes for strict engine decoding.
// Empty bytes represent missing evidence, not an empty JSON object.
type RawInputs struct{ Task, Member, State, Policy []byte }

// Tuple is one explicitly catalogued runtime/model/effort and its policy facts.
type Tuple struct {
	ID             string `json:"id"`
	Runtime        string `json:"runtime"`
	Vendor         string `json:"vendor"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	CapabilityRank int    `json:"capability_rank"`
	RelativeCost   int    `json:"relative_cost"`
	QuotaScope     string `json:"quota_scope"`
	Billing        string `json:"billing"`
}

// Classification is a detached, validated snapshot. Eligible contains only
// role-admitted, below-ceiling, included, quota-clear choices (and, when quota
// forces a switch, choices strictly cheaper than Current).
type Classification struct {
	TaskID                              string
	Current                             Tuple
	Eligible                            []Tuple
	PreferredTupleIDs                   []string
	SessionLive                         bool
	ContextUsedBPS, ContextRetentionBPS int
	RuleID, Reason                      string
}

// Classifier is a compile-time seam. Implementations must be deterministic and
// side-effect free. Recommend validates the entire returned Result; this seam
// provides no launch authority or permission to override a hard gate.
type Classifier interface{ Classify(Classification) Result }

// RuleTable is the default deterministic classifier; no model plugin is loaded.
type RuleTable struct{}

// Result uses a pointer to preserve context_reset=false for keep, while omitting
// context_reset and all targets from non-recommendations.
type Result struct {
	Contract           string   `json:"contract"`
	Status             string   `json:"status"`
	TaskID             string   `json:"task_id,omitempty"`
	RuleID             string   `json:"rule_id"`
	Reason             string   `json:"reason"`
	ExecutionAuthority bool     `json:"execution_authority"`
	Action             string   `json:"action,omitempty"`
	Selected           string   `json:"selected,omitempty"`
	Runtime            string   `json:"runtime,omitempty"`
	Model              string   `json:"model,omitempty"`
	Effort             string   `json:"effort,omitempty"`
	ContextReset       *bool    `json:"context_reset,omitempty"`
	Code               string   `json:"code,omitempty"`
	Missing            []string `json:"missing,omitempty"`
}

// ExitCode maps the four contract statuses; unknown statuses fail closed.
func (r Result) ExitCode() int {
	switch r.Status {
	case "recommended":
		return 0
	case "insufficient_evidence":
		return 2
	case "defer":
		return 3
	default:
		return 1
	}
}

type task struct {
	Contract        string `json:"contract"`
	ElementID       string `json:"element_id"`
	Project         string `json:"project"`
	Class           string `json:"class"`
	EstimateMinutes int    `json:"estimate_minutes"`
	ReviewPolicy    string `json:"review_policy"`
}

type configuration struct {
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type member struct {
	Contract string        `json:"contract"`
	Role     string        `json:"role"`
	Current  configuration `json:"current"`
}

type state struct {
	Contract       string  `json:"contract"`
	SessionLive    bool    `json:"session_live"`
	ContextUsedBPS int     `json:"context_used_bps"`
	ObservedAt     string  `json:"observed_at"`
	ExpiresAt      string  `json:"expires_at"`
	ProviderQuotas []quota `json:"provider_quotas"`
}

type quota struct {
	Scope      string `json:"scope"`
	UsedBPS    int    `json:"used_bps"`
	ResetAt    string `json:"reset_at"`
	ObservedAt string `json:"observed_at"`
	ExpiresAt  string `json:"expires_at"`
}

type policy struct {
	Contract            string      `json:"contract"`
	Catalog             []Tuple     `json:"catalog"`
	Roles               []role      `json:"roles"`
	QuotaThresholds     []threshold `json:"quota_thresholds"`
	ContextRetentionBPS int         `json:"context_retention_bps"`
	Rules               []rule      `json:"rules"`
}

type role struct {
	Role            string   `json:"role"`
	CeilingRank     int      `json:"ceiling_rank"`
	AllowedTupleIDs []string `json:"allowed_tuple_ids"`
}

type threshold struct {
	Scope   string `json:"scope"`
	UsedBPS int    `json:"used_bps"`
}

type rule struct {
	ID                 string   `json:"id"`
	Project            string   `json:"project"`
	Role               string   `json:"role"`
	Class              string   `json:"class"`
	ReviewPolicy       string   `json:"review_policy"`
	EstimateMinMinutes int      `json:"estimate_min_minutes"`
	EstimateMaxMinutes int      `json:"estimate_max_minutes"`
	PreferredTupleIDs  []string `json:"preferred_tuple_ids"`
}
