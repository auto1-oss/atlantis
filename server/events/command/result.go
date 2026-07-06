// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package command

// Result is the result of running a Command.
type Result struct {
	Error          error
	Failure        string
	ProjectResults []ProjectResult
	// PlansDeleted is true if all plans created during this command were
	// deleted. This happens if automerging is enabled and one project has an
	// error since automerging requires all plans to succeed.
	PlansDeleted bool
	// Warning is an informational message rendered at the top of the command
	// comment. It does NOT count as an error (see HasErrors) so it does not
	// affect commit status. Used e.g. to report projects skipped by partial apply.
	Warning string
}

// HasErrors returns true if there were any errors during the execution,
// even if it was only in one project.
func (c Result) HasErrors() bool {
	if c.Error != nil || c.Failure != "" {
		return true
	}
	for _, r := range c.ProjectResults {
		if !r.IsSuccessful() {
			return true
		}
	}
	return false
}
