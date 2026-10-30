// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package models

import (
	"os"
	"os/exec"
	"syscall"
)

func startProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func signalProcessGroup(p *os.Process, sig syscall.Signal) error {
	return syscall.Kill(-p.Pid, sig)
}
