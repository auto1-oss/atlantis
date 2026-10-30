// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package models

import (
	"os"
	"os/exec"
	"syscall"
)

func startProcessGroup(*exec.Cmd) {}

// signalProcessGroup kills p, because Windows cannot deliver SIGINT to
// another process.
func signalProcessGroup(p *os.Process, _ syscall.Signal) error {
	return p.Kill()
}
