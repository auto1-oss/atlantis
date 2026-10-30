// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package models

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/runatlantis/atlantis/server/logging"
)

// InterruptGracePeriod is how long an interrupted process has to exit before
// it is killed. Terraform uses it to finish in-flight provider calls and
// release the state lock.
const InterruptGracePeriod = time.Minute

// StartInterruptible starts cmd. If interrupt is cancelled before cmd exits,
// cmd and the processes it started receive SIGINT, and SIGKILL once
// InterruptGracePeriod has passed. The returned function must be called after
// cmd has exited. A nil interrupt starts cmd as is.
func StartInterruptible(log logging.SimpleLogging, interrupt context.Context, cmd *exec.Cmd) (func(), error) {
	if interrupt == nil {
		return func() {}, cmd.Start()
	}
	if interrupt.Err() != nil {
		return func() {}, context.Cause(interrupt)
	}
	startProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}

	exited := make(chan struct{})
	go func() {
		select {
		case <-exited:
			return
		case <-interrupt.Done():
		}
		log.Info("interrupting %q, %s", cmd.Path, context.Cause(interrupt))
		if err := signalProcessGroup(cmd.Process, syscall.SIGINT); err != nil {
			log.Warn("sending interrupt to %q: %s", cmd.Path, err)
		}
		select {
		case <-exited:
		case <-time.After(InterruptGracePeriod):
			log.Warn("killing %q, still running %s after interrupt", cmd.Path, InterruptGracePeriod)
			if err := signalProcessGroup(cmd.Process, syscall.SIGKILL); err != nil {
				log.Warn("killing %q: %s", cmd.Path, err)
			}
		}
	}()
	return func() { close(exited) }, nil
}

// WrapInterrupted prefixes err with the reason interrupt was cancelled, if it
// was, so that users see why their command failed.
func WrapInterrupted(interrupt context.Context, err error) error {
	if err == nil || interrupt == nil || interrupt.Err() == nil {
		return err
	}
	return fmt.Errorf("%w: %w", context.Cause(interrupt), err)
}
