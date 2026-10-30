// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package models_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runatlantis/atlantis/server/core/runtime/models"
	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/jobs/mocks"
	"github.com/runatlantis/atlantis/server/logging"
	. "github.com/runatlantis/atlantis/testing"
)

var errTestCancelled = errors.New("cancelled by test")

func TestShellCommandRunner_InterruptStopsProcessGroup(t *testing.T) {
	interrupt, cancel := context.WithCancelCause(context.Background())
	ctx := command.ProjectContext{Log: logging.NewNoopLogger(t), Interrupt: interrupt}
	dir := t.TempDir()
	// The shell runs sleep as a child, so the child only stops if the whole
	// process group is signalled.
	runner := models.NewShellCommandRunner(nil, "sleep 30; touch finished", os.Environ(), dir, false, mocks.NewMockProjectCommandOutputHandler())

	time.AfterFunc(200*time.Millisecond, func() { cancel(errTestCancelled) })
	start := time.Now()
	_, err := runner.Run(ctx)

	Assert(t, time.Since(start) < 10*time.Second, "interrupted command took %s to exit", time.Since(start))
	Assert(t, errors.Is(err, errTestCancelled), "expected error to wrap the cancellation cause, got %v", err)
	_, statErr := os.Stat(filepath.Join(dir, "finished"))
	Assert(t, os.IsNotExist(statErr), "command continued after being interrupted")
}

func TestShellCommandRunner_AlreadyInterruptedDoesNotStart(t *testing.T) {
	interrupt, cancel := context.WithCancelCause(context.Background())
	cancel(errTestCancelled)
	ctx := command.ProjectContext{Log: logging.NewNoopLogger(t), Interrupt: interrupt}
	dir := t.TempDir()
	runner := models.NewShellCommandRunner(nil, "touch started", os.Environ(), dir, false, mocks.NewMockProjectCommandOutputHandler())

	_, err := runner.Run(ctx)

	Assert(t, errors.Is(err, errTestCancelled), "expected error to wrap the cancellation cause, got %v", err)
	_, statErr := os.Stat(filepath.Join(dir, "started"))
	Assert(t, os.IsNotExist(statErr), "command started although it was already interrupted")
}

func TestShellCommandRunner_NilInterruptRunsNormally(t *testing.T) {
	ctx := command.ProjectContext{Log: logging.NewNoopLogger(t)}
	runner := models.NewShellCommandRunner(nil, "echo hi", os.Environ(), t.TempDir(), false, mocks.NewMockProjectCommandOutputHandler())

	out, err := runner.Run(ctx)

	Ok(t, err)
	Equals(t, "hi\n", out)
}
