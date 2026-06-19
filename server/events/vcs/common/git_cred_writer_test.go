// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package common_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/runatlantis/atlantis/server/events/vcs/common"
	"github.com/runatlantis/atlantis/server/logging"
	. "github.com/runatlantis/atlantis/testing"
)

// Test that we write the file as expected
func TestWriteGitCreds_WriteFile(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteGitCreds("user", "token", "hostname", tmp, logger, false)
	Ok(t, err)

	expContents := `https://user:token@hostname` // #nosec G101 -- test fixture, not real credentials

	actContents, err := os.ReadFile(filepath.Join(tmp, ".git-credentials"))
	Ok(t, err)
	Equals(t, expContents, string(actContents))
}

// Test that if the file already exists and it doesn't have the line we would
// have written, we write it.
func TestWriteGitCreds_Appends(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	credsFile := filepath.Join(tmp, ".git-credentials")
	err := os.WriteFile(credsFile, []byte("contents"), 0600)
	Ok(t, err)

	err = common.WriteGitCreds("user", "token", "hostname", tmp, logger, false)
	Ok(t, err)

	expContents := "contents\nhttps://user:token@hostname" // #nosec G101 -- test fixture, not real credentials
	actContents, err := os.ReadFile(filepath.Join(tmp, ".git-credentials"))
	Ok(t, err)
	Equals(t, expContents, string(actContents))
}

// Test that if the file already exists and it already has the line expected
// we do nothing.
func TestWriteGitCreds_NoModification(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	credsFile := filepath.Join(tmp, ".git-credentials")
	contents := "line1\nhttps://user:token@hostname\nline2" // #nosec G101 -- test fixture, not real credentials
	err := os.WriteFile(credsFile, []byte(contents), 0600)
	Ok(t, err)

	err = common.WriteGitCreds("user", "token", "hostname", tmp, logger, false)
	Ok(t, err)
	actContents, err := os.ReadFile(filepath.Join(tmp, ".git-credentials"))
	Ok(t, err)
	Equals(t, contents, string(actContents))
}

// Test that the github app credentials get replaced.
func TestWriteGitCreds_ReplaceApp(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	credsFile := filepath.Join(tmp, ".git-credentials")
	contents := "line1\nhttps://x-access-token:v1.87dddddddddddddddd@github.com\nline2" // #nosec G101 -- test fixture, not real credentials
	err := os.WriteFile(credsFile, []byte(contents), 0600)
	Ok(t, err)

	err = common.WriteGitCreds("x-access-token", "token", "github.com", tmp, logger, true)
	Ok(t, err)
	expContents := "line1\nhttps://x-access-token:token@github.com\nline2" // #nosec G101 -- test fixture, not real credentials
	actContents, err := os.ReadFile(filepath.Join(tmp, ".git-credentials"))
	Ok(t, err)
	Equals(t, expContents, string(actContents))
}

// Test that the github app credential gets added even if there are other credentials.
func TestWriteGitCreds_AppendAppWhenFileNotEmpty(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	credsFile := filepath.Join(tmp, ".git-credentials")
	contents := "line1\nhttps://user:token@host.com\nline2" // #nosec G101 -- test fixture, not real credentials
	err := os.WriteFile(credsFile, []byte(contents), 0600)
	Ok(t, err)

	err = common.WriteGitCreds("x-access-token", "token", "github.com", tmp, logger, true)
	Ok(t, err)
	expContents := "line1\nhttps://user:token@host.com\nline2\nhttps://x-access-token:token@github.com" // #nosec G101 -- test fixture, not real credentials
	actContents, err := os.ReadFile(filepath.Join(tmp, ".git-credentials"))
	Ok(t, err)
	Equals(t, expContents, string(actContents))
}

// Test that the github app credentials get updated when cred file is empty.
func TestWriteGitCreds_AppendApp(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	credsFile := filepath.Join(tmp, ".git-credentials")
	contents := ""
	err := os.WriteFile(credsFile, []byte(contents), 0600)
	Ok(t, err)

	err = common.WriteGitCreds("x-access-token", "token", "github.com", tmp, logger, true)
	Ok(t, err)
	expContents := "https://x-access-token:token@github.com" // #nosec G101 -- test fixture, not real credentials
	actContents, err := os.ReadFile(filepath.Join(tmp, ".git-credentials"))
	Ok(t, err)
	Equals(t, expContents, string(actContents))
}

// Test that if we can't read the existing file to see if the contents will be
// the same that we just error out.
func TestWriteGitCreds_ErrIfCannotRead(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	credsFile := filepath.Join(tmp, ".git-credentials")
	err := os.WriteFile(credsFile, []byte("can't see me!"), 0000)
	Ok(t, err)

	expErr := fmt.Sprintf("open %s: permission denied", credsFile)
	actErr := common.WriteGitCreds("user", "token", "hostname", tmp, logger, false)
	ErrContains(t, expErr, actErr)
}

// Test that if we can't write, we error out.
func TestWriteGitCreds_ErrIfCannotWrite(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	credsFile := "/this/dir/does/not/exist/.git-credentials" // nolint: gosec
	expErr := fmt.Sprintf("writing generated .git-credentials file with user, token and hostname to %s: open %s: no such file or directory", credsFile, credsFile)
	actErr := common.WriteGitCreds("user", "token", "hostname", "/this/dir/does/not/exist", logger, false)
	ErrEquals(t, expErr, actErr)
}

// Test that git is actually configured to use the credentials
func TestWriteGitCreds_ConfigureGitCredentialHelper(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteGitCreds("user", "token", "hostname", tmp, logger, false)
	Ok(t, err)

	expOutput := `store`
	actOutput, err := exec.Command("git", "config", "--global", "credential.helper").Output()
	Ok(t, err)
	Equals(t, expOutput+"\n", string(actOutput))
}

// Test that git is configured to use https instead of ssh
func TestWriteGitCreds_ConfigureGitUrlOverride(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteGitCreds("user", "token", "hostname", tmp, logger, false)
	Ok(t, err)

	expOutput := `ssh://git@hostname`
	actOutput, err := exec.Command("git", "config", "--global", "url.https://user@hostname.insteadof").Output()
	Ok(t, err)
	Equals(t, expOutput+"\n", string(actOutput))
}

// Test that WriteOrgGitCreds creates the org-specific config file with the correct content.
func TestWriteOrgGitCreds_CreatesConfigFile(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteOrgGitCreds("x-access-token", "ghs_token", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)

	orgConfigFile := filepath.Join(tmp, ".gitconfig-org-my-org")
	actContents, err := os.ReadFile(orgConfigFile)
	Ok(t, err)

	expContents := "[url \"https://x-access-token:ghs_token@github.com/my-org/\"]\n" + // #nosec G101 -- test fixture
		"\tinsteadOf = ssh://git@github.com/my-org/\n" +
		"\tinsteadOf = https://github.com/my-org/\n"
	Equals(t, expContents, string(actContents))
}

// Test that WriteOrgGitCreds adds include.path to ~/.gitconfig on first call.
func TestWriteOrgGitCreds_AddsIncludePath(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteOrgGitCreds("x-access-token", "ghs_token", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)

	orgConfigFile := filepath.Join(tmp, ".gitconfig-org-my-org")
	actOutput, err := exec.Command("git", "config", "--global", "--get-all", "include.path").Output()
	Ok(t, err)
	Assert(t, len(actOutput) > 0, "include.path should be set")
	Equals(t, orgConfigFile+"\n", string(actOutput))
}

// Test that calling WriteOrgGitCreds twice does not add include.path twice.
func TestWriteOrgGitCreds_NoduplicateIncludePath(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteOrgGitCreds("x-access-token", "ghs_token", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)
	err = common.WriteOrgGitCreds("x-access-token", "ghs_token2", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)

	orgConfigFile := filepath.Join(tmp, ".gitconfig-org-my-org")
	actOutput, err := exec.Command("git", "config", "--global", "--get-all", "include.path").Output()
	Ok(t, err)
	// Should appear exactly once
	lines := filepath.SplitList(string(actOutput))
	count := 0
	for _, line := range lines {
		if line == orgConfigFile {
			count++
		}
	}
	Assert(t, count <= 1, "include.path should contain the org config file at most once")
}

// Test that WriteOrgGitCreds updates the file with a new token on repeat calls.
func TestWriteOrgGitCreds_UpdatesTokenOnRotation(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteOrgGitCreds("x-access-token", "token1", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)
	err = common.WriteOrgGitCreds("x-access-token", "token2", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)

	orgConfigFile := filepath.Join(tmp, ".gitconfig-org-my-org")
	actContents, err := os.ReadFile(orgConfigFile)
	Ok(t, err)

	Assert(t, !contains(string(actContents), "token1"), "old token should be replaced")
	Assert(t, contains(string(actContents), "token2"), "new token should be present")
}

// Test that WriteOrgGitCreds errors if home dir doesn't exist.
func TestWriteOrgGitCreds_ErrIfBadHomeDir(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	err := common.WriteOrgGitCreds("x-access-token", "token", "github.com", "my-org/", "/this/does/not/exist", logger) // #nosec G101 -- test fixture
	Assert(t, err != nil, "should return error for non-existent home dir")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
