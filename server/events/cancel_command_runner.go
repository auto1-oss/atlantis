// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/events/vcs"
)

const cancelComment = "Cancelled all queued operations, interrupted running plans and released working directory locks for this pull request.\n" +
	"New operations can now be started. Running applies continue to completion."

func NewCancelCommandRunner(
	vcsClient vcs.Client,
	projectCmdRunner ProjectCommandRunner,
	pullUpdater *PullUpdater,
	workingDirLocker WorkingDirLocker,
	silenceNoProjects bool,
) *CancelCommandRunner {
	return &CancelCommandRunner{
		VCSClient:         vcsClient,
		ProjectCmdRunner:  projectCmdRunner,
		PullUpdater:       pullUpdater,
		WorkingDirLocker:  workingDirLocker,
		SilenceNoProjects: silenceNoProjects,
	}
}

type CancelCommandRunner struct {
	VCSClient         vcs.Client
	ProjectCmdRunner  ProjectCommandRunner
	PullUpdater       *PullUpdater
	WorkingDirLocker  WorkingDirLocker
	SilenceNoProjects bool
}

func (c *CancelCommandRunner) Run(ctx *command.Context, cmd *CommentCommand) {
	if c.ProjectCmdRunner == nil {
		ctx.Log.Err("ProjectCmdRunner is nil")
		return
	}

	// Get the DefaultProjectCommandRunner to access the process tracker
	defaultRunner, ok := c.ProjectCmdRunner.(*DefaultProjectCommandRunner)
	if !ok {
		ctx.Log.Err("ProjectCmdRunner is not a DefaultProjectCommandRunner")
		return
	}

	if defaultRunner.CancellationTracker == nil {
		ctx.Log.Err("CancellationTracker is nil")
		return
	}

	ctx.Log.Info("cancelling queued operations and running plans")
	waitForCancelledRuns(ctx.Log, defaultRunner.CancellationTracker.Cancel(ctx.Pull, ErrCancelledByCommand))

	// Clean up working directory locks for this pull request
	if defaultRunner.WorkingDirLocker != nil {
		defaultRunner.WorkingDirLocker.UnlockByPull(ctx.Pull.BaseRepo.FullName, ctx.Pull.Num)
		ctx.Log.Debug("Released working directory locks for pull request")
	}

	ctx.Log.Info("cancelled queued operations and running plans")
	if err := c.VCSClient.CreateComment(ctx.Log, ctx.Pull.BaseRepo, ctx.Pull.Num, cancelComment, ""); err != nil {
		ctx.Log.Err("unable to comment: %s", err)
	}
}
