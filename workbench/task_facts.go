package workbench

import "context"

// TaskFacts preserves an observed native task projection, not eligibility.
// Empty assignee and unfamiliar status are data, never permission or absence.
type TaskFacts struct {
	ID, Title, Status, Assignee string
}

// TaskObservation identifies captured task-object bytes only. It deliberately
// has no verified revision, owner witness, source lifetime, target or gates.
type TaskObservation struct {
	Facts            TaskFacts
	ProjectionSHA256 string
}

// TaskFactsSource is a trusted, read-only partial observation seam. The native
// ID is separately admitted by the embedding; it is not assumed to be a request
// RevisionRef.Object. This port does not implement positive OwnerReader input.
type TaskFactsSource interface {
	Observe(context.Context, string) (TaskObservation, error)
}
