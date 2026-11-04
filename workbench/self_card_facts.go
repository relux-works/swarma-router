package workbench

import "context"

// SelfCardFacts contains desired self declarations, never current authority.
// Project is nil when the admitted card declares no project.
type SelfCardFacts struct {
	MemberKey, ResolvedName, Role string
	Project                       *string
}

// SelfCardObservation binds only the selected source bytes. It has no verified
// revision, observation time, expiry, profile, runtime, target or eligibility.
type SelfCardObservation struct {
	Facts        SelfCardFacts
	SourceSHA256 string
}

// SelfCardFactsSource has no caller-selected member, path, view or identity.
// Its trusted configuration selects one unsigned self declaration source.
type SelfCardFactsSource interface {
	Observe(context.Context) (SelfCardObservation, error)
}
