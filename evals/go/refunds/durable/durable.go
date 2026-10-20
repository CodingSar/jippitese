// Package durable journals the steps of a workflow run so a run can be
// resumed or replayed from its history without repeating its side effects.
package durable

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrReplay is a history that does not match the run replaying it.
var ErrReplay = errors.New("history does not match the run")

// Event is one completed step of a run.
type Event struct {
	Step   string
	Result json.RawMessage
}

// Journal is the history of one run: the workflow version it was recorded
// under and its completed steps.
type Journal struct {
	Version int
	Events  []Event
	next    int
}

// New is an empty journal for a run of the given workflow version.
func New(version int) *Journal { return &Journal{Version: version} }

// Replay is a copy of j that replays its history from the first step.
func (j *Journal) Replay() *Journal {
	return &Journal{Version: j.Version, Events: append([]Event(nil), j.Events...)}
}

// Check fails with ErrReplay when j was recorded under another version.
func (j *Journal) Check(version int) error {
	if j.Version != version {
		return fmt.Errorf("%w: recorded by version %d, running version %d", ErrReplay, j.Version, version)
	}
	return nil
}

// Step runs f as the next step of the run named name and records its
// result. When the history already holds that step, its recorded result
// is returned and f does not run. A failed step is not recorded.
func Step[T any](j *Journal, name string, f func() (T, error)) (T, error) {
	var v T
	if j.next < len(j.Events) {
		e := j.Events[j.next]
		if e.Step != name {
			return v, fmt.Errorf("%w: step %d is %q in the history, %q in the run", ErrReplay, j.next+1, e.Step, name)
		}
		j.next++
		err := json.Unmarshal(e.Result, &v)
		return v, err
	}
	v, err := f()
	if err != nil {
		return v, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	j.Events = append(j.Events, Event{Step: name, Result: b})
	j.next++
	return v, nil
}
