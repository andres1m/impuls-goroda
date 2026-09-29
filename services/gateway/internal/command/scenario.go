package command

import "errors"

var ErrScenarioCompleted = errors.New("scenario is already completed")

type ScenarioVersionConflict struct {
	Current int64
}

func (e *ScenarioVersionConflict) Error() string { return "scenario version is stale" }
