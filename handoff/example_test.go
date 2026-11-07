package handoff_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"relux.works/swarma-router/handoff"
)

// planFixture is an in-memory teaching adapter, not a source validator for use
// with real tasks. It has no native, network or filesystem capabilities.
type planFixture struct {
	sourceAvailable         bool
	checks, posts, verifies int
}

var _ handoff.Adapter = (*planFixture)(nil)

func (a *planFixture) ValidateSources(ctx context.Context, r handoff.Request, _ handoff.Policy) string {
	a.checks++
	if ctx.Err() != nil || !a.sourceAvailable {
		return "source_unavailable"
	}
	if r.Action != "plan" {
		return "fixture_plan_only"
	}
	return ""
}

func (a *planFixture) Post(context.Context, handoff.Request, handoff.Policy, string) (string, error) {
	a.posts++
	return "", errors.New("fixture has no post capability")
}

func (a *planFixture) VerifyEvent(context.Context, handoff.Request, handoff.Policy, string, string) string {
	a.verifies++
	return "fixture has no event source"
}

// ExampleEngine shows static adapter injection for the shipped reduced contract.
// All bindings and source availability below are synthetic. The same valid
// request refuses when only its source becomes unavailable; no effect port runs.
func ExampleEngine() {
	r := handoff.Request{
		Contract: handoff.Contract, Action: "plan", CorrelationID: "example-plan",
		Project: "example", TaskID: "TASK-260102-example", TaskTitle: "Example task",
		Member: "recipient", Room: "example", Topic: "work", ReplyTo: "$example-reply",
	}
	p := handoff.Policy{
		Contract: handoff.Contract, Project: r.Project,
		ProjectRoot: "/example", BoardDir: "/example/.task-board",
		Server: "example.test", Sender: "caller", Room: r.Room, RoomID: "!example-room",
		Topic: r.Topic, ReplyTo: r.ReplyTo,
		Tasks: []handoff.TaskBinding{{ID: r.TaskID, Title: r.TaskTitle, Members: []string{r.Member}}},
	}
	a := &planFixture{sourceAvailable: true}
	journalOpens := 0
	e := handoff.Engine{
		Adapter: a,
		OpenJournal: func(string) (handoff.Journal, error) {
			journalOpens++
			return nil, errors.New("fixture has no journal capability")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	planned := e.Execute(ctx, r, p, "") // plan does not open a journal path.
	fmt.Println(planned.Status, planned.ExitCode(), "notice:", planned.Notice != "", "event:", planned.EventID != "")
	a.sourceAvailable = false
	refused := e.Execute(ctx, r, p, "")
	fmt.Println(refused.Status, refused.ExitCode(), refused.Reason, "notice:", refused.Notice != "")
	fmt.Println("checks:", a.checks, "posts:", a.posts, "verifies:", a.verifies, "journal opens:", journalOpens)
	// Output:
	// planned 0 notice: true event: false
	// refused 2 source_unavailable notice: false
	// checks: 2 posts: 0 verifies: 0 journal opens: 0
}
