// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/logging"
)

var (
	ErrCancelledByCommand = errors.New("operation cancelled via `atlantis cancel` command")
	ErrCancelledByUnlock  = errors.New("operation cancelled via `atlantis unlock` command")
	ErrCancelledPullClose = errors.New("operation cancelled because the pull request was closed")
)

// CancelledRunsWaitTimeout bounds how long a caller waits for cancelled runs
// to finish. It covers the grace period a running plan gets to stop after
// being interrupted, plus time to post its results.
const CancelledRunsWaitTimeout = 2 * time.Minute

// CancellationTracker tracks the plan and apply runs that are active for each
// pull request so that they can be cancelled. Cancelling a run skips its
// queued projects; plan runs also have their running processes interrupted.
type CancellationTracker interface {
	// Start registers a run of cmdName for pull. The returned context is
	// cancelled, with the cancellation reason as its cause, when the run is
	// cancelled. The returned function must be called when the run ends.
	//
	// Starting a plan run cancels the plan runs already active for pull at a
	// commit other than pull.HeadCommit. The returned channel is closed once
	// they have ended.
	Start(pull models.PullRequest, cmdName command.Name) (context.Context, func(), <-chan struct{})
	// Cancel cancels every active run for pull. The returned channel is
	// closed once the cancelled plan runs have ended; apply runs are not
	// waited for because their running projects complete.
	Cancel(pull models.PullRequest, cause error) <-chan struct{}
}

type trackedRun struct {
	cmdName    command.Name
	headCommit string
	cancel     context.CancelCauseFunc
	done       chan struct{}
}

type DefaultCancellationTracker struct {
	mutex sync.Mutex
	runs  map[string]map[*trackedRun]struct{}
}

func NewCancellationTracker() *DefaultCancellationTracker {
	return &DefaultCancellationTracker{
		runs: make(map[string]map[*trackedRun]struct{}),
	}
}

func (p *DefaultCancellationTracker) Start(pull models.PullRequest, cmdName command.Name) (context.Context, func(), <-chan struct{}) {
	ctx, cancel := context.WithCancelCause(context.Background())
	run := &trackedRun{cmdName: cmdName, headCommit: pull.HeadCommit, cancel: cancel, done: make(chan struct{})}
	key := pullKey(pull)

	// Outdated plans are cancelled under the same lock that registers this
	// run, so of several plans started for different commits only the last
	// one survives.
	p.mutex.Lock()
	var superseded []*trackedRun
	if cmdName == command.Plan {
		superseded = p.cancelLocked(key, func(other *trackedRun) error {
			if other.cmdName != command.Plan || other.headCommit == pull.HeadCommit {
				return nil
			}
			return fmt.Errorf("plan for commit %s cancelled because commit %s was pushed", shortSHA(other.headCommit), shortSHA(pull.HeadCommit))
		})
	}
	if p.runs[key] == nil {
		p.runs[key] = make(map[*trackedRun]struct{})
	}
	p.runs[key][run] = struct{}{}
	p.mutex.Unlock()

	var once sync.Once
	end := func() {
		once.Do(func() {
			p.mutex.Lock()
			delete(p.runs[key], run)
			if len(p.runs[key]) == 0 {
				delete(p.runs, key)
			}
			p.mutex.Unlock()
			cancel(nil)
			close(run.done)
		})
	}
	return ctx, end, whenEnded(superseded)
}

func (p *DefaultCancellationTracker) Cancel(pull models.PullRequest, cause error) <-chan struct{} {
	p.mutex.Lock()
	cancelled := p.cancelLocked(pullKey(pull), func(*trackedRun) error { return cause })
	p.mutex.Unlock()
	return whenEnded(cancelled)
}

// cancelLocked cancels each active run under key for which causeFor returns
// a non-nil cause, and returns the cancelled plan runs. p.mutex must be held.
func (p *DefaultCancellationTracker) cancelLocked(key string, causeFor func(*trackedRun) error) []*trackedRun {
	var cancelledPlans []*trackedRun
	for run := range p.runs[key] {
		if cause := causeFor(run); cause != nil {
			run.cancel(cause)
			if run.cmdName == command.Plan {
				cancelledPlans = append(cancelledPlans, run)
			}
		}
	}
	return cancelledPlans
}

// whenEnded returns a channel that is closed once all runs have ended.
func whenEnded(runs []*trackedRun) <-chan struct{} {
	allDone := make(chan struct{})
	go func() {
		for _, run := range runs {
			<-run.done
		}
		close(allDone)
	}()
	return allDone
}

// startCancellableRun registers a run with tracker, which may be nil, and
// waits for the outdated plans it supersedes to end.
func startCancellableRun(log logging.SimpleLogging, tracker CancellationTracker, pull models.PullRequest, cmdName command.Name) (context.Context, func()) {
	if tracker == nil {
		return context.Background(), func() {}
	}
	runCtx, endRun, superseded := tracker.Start(pull, cmdName)
	waitForCancelledRuns(log, superseded)
	return runCtx, endRun
}

// waitForCancelledRuns waits until the runs behind done have ended, giving up
// after CancelledRunsWaitTimeout.
func waitForCancelledRuns(log logging.SimpleLogging, done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(CancelledRunsWaitTimeout):
		log.Warn("cancelled runs still active after %s, continuing", CancelledRunsWaitTimeout)
	}
}

// runCause returns why runCtx was cancelled, or nil if it was not.
func runCause(runCtx context.Context) error {
	if runCtx.Err() == nil {
		return nil
	}
	return context.Cause(runCtx)
}

func pullKey(pull models.PullRequest) string {
	return fmt.Sprintf("%s#%d", pull.BaseRepo.FullName, pull.Num)
}
