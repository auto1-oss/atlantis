// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"testing"
	"time"

	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("channel was not closed")
	}
}

func requireOpen(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("channel was closed")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancellationTracker_CancelSetsCause(t *testing.T) {
	tracker := NewCancellationTracker()
	pull := models.PullRequest{Num: 1}
	runCtx, endRun, _ := tracker.Start(pull, command.Plan)

	done := tracker.Cancel(pull, ErrCancelledByUnlock)

	require.Error(t, runCtx.Err())
	require.ErrorIs(t, context.Cause(runCtx), ErrCancelledByUnlock)
	requireOpen(t, done)
	endRun()
	requireClosed(t, done)
}

func TestCancellationTracker_CancelWithoutActiveRunDoesNotAffectLaterRuns(t *testing.T) {
	tracker := NewCancellationTracker()
	pull := models.PullRequest{Num: 1}

	requireClosed(t, tracker.Cancel(pull, ErrCancelledByUnlock))

	runCtx, endRun, _ := tracker.Start(pull, command.Plan)
	defer endRun()
	assert.NoError(t, runCtx.Err())
}

func TestCancellationTracker_CancelOnlyAffectsItsPull(t *testing.T) {
	tracker := NewCancellationTracker()
	pull := models.PullRequest{Num: 1}
	otherPull := models.PullRequest{Num: 2}
	otherCtx, endOther, _ := tracker.Start(otherPull, command.Plan)
	defer endOther()

	requireClosed(t, tracker.Cancel(pull, ErrCancelledByCommand))

	assert.NoError(t, otherCtx.Err())
}

func TestCancellationTracker_CancelDoesNotWaitForApplies(t *testing.T) {
	tracker := NewCancellationTracker()
	pull := models.PullRequest{Num: 1}
	applyCtx, endApply, _ := tracker.Start(pull, command.Apply)
	defer endApply()

	requireClosed(t, tracker.Cancel(pull, ErrCancelledByCommand))

	require.ErrorIs(t, context.Cause(applyCtx), ErrCancelledByCommand)
}

func TestCancellationTracker_PlanStartSupersedesOutdatedPlans(t *testing.T) {
	tracker := NewCancellationTracker()
	oldPull := models.PullRequest{Num: 1, HeadCommit: "1111111aaaa"}
	newPull := models.PullRequest{Num: 1, HeadCommit: "2222222bbbb"}

	oldPlanCtx, endOldPlan, _ := tracker.Start(oldPull, command.Plan)
	samePlanCtx, endSamePlan, _ := tracker.Start(newPull, command.Plan)
	defer endSamePlan()
	applyCtx, endApply, _ := tracker.Start(oldPull, command.Apply)
	defer endApply()

	newPlanCtx, endNewPlan, superseded := tracker.Start(newPull, command.Plan)
	defer endNewPlan()

	require.Error(t, oldPlanCtx.Err())
	assert.EqualError(t, context.Cause(oldPlanCtx), "plan for commit 1111111 cancelled because commit 2222222 was pushed")
	assert.NoError(t, samePlanCtx.Err(), "a plan for the same commit must not be cancelled")
	assert.NoError(t, applyCtx.Err(), "an apply must not be cancelled by a new commit")
	assert.NoError(t, newPlanCtx.Err())
	requireOpen(t, superseded)
	endOldPlan()
	requireClosed(t, superseded)
}

func TestCancellationTracker_OnlyNewestOfRapidCommitsSurvives(t *testing.T) {
	tracker := NewCancellationTracker()
	commitA := models.PullRequest{Num: 1, HeadCommit: "aaaaaaa1"}
	commitB := models.PullRequest{Num: 1, HeadCommit: "bbbbbbb2"}
	commitC := models.PullRequest{Num: 1, HeadCommit: "ccccccc3"}

	ctxA, endA, _ := tracker.Start(commitA, command.Plan)
	ctxB, endB, waitB := tracker.Start(commitB, command.Plan)
	ctxC, endC, waitC := tracker.Start(commitC, command.Plan)
	defer endC()

	require.Error(t, ctxA.Err())
	require.Error(t, ctxB.Err(), "B must be superseded by C while it waits for A")
	assert.NoError(t, ctxC.Err())

	endA()
	requireClosed(t, waitB)
	requireOpen(t, waitC)
	endB()
	requireClosed(t, waitC)
}

func TestCancellationTracker_ApplyStartDoesNotCancelPlans(t *testing.T) {
	tracker := NewCancellationTracker()
	planCtx, endPlan, _ := tracker.Start(models.PullRequest{Num: 1, HeadCommit: "aaaaaaa1"}, command.Plan)
	defer endPlan()

	_, endApply, superseded := tracker.Start(models.PullRequest{Num: 1, HeadCommit: "bbbbbbb2"}, command.Apply)
	defer endApply()

	assert.NoError(t, planCtx.Err())
	requireClosed(t, superseded)
}

func TestCancellationTracker_EndRunIsIdempotent(t *testing.T) {
	tracker := NewCancellationTracker()
	pull := models.PullRequest{Num: 1}
	_, endRun, _ := tracker.Start(pull, command.Plan)

	endRun()
	endRun()

	assert.Empty(t, tracker.runs)
}

func TestStartCancellableRun_NilTracker(t *testing.T) {
	runCtx, endRun := startCancellableRun(logging.NewNoopLogger(t), nil, models.PullRequest{Num: 1}, command.Plan)
	endRun()
	assert.NoError(t, runCtx.Err())
}
