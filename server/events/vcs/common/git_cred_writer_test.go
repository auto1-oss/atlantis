// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package common_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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
	err := os.Mkdir(credsFile, 0700)
	Ok(t, err)

	actErr := common.WriteGitCreds("user", "token", "hostname", tmp, logger, false)
	ErrContains(t, "reading "+credsFile, actErr)
}

// Test that if we can't write, we error out.
func TestWriteGitCreds_ErrIfCannotWrite(t *testing.T) {
	logger := logging.NewNoopLogger(t)

	nonExistentDir := filepath.Join(
		t.TempDir(),
		"does",
		"not",
		"exist",
	)

	actErr := common.WriteGitCreds(
		"user",
		"token",
		"hostname",
		nonExistentDir,
		logger,
		false,
	)

	ErrContains(
		t,
		"writing generated .git-credentials file with user, token and hostname",
		actErr,
	)
	Assert(t, errors.Is(actErr, os.ErrNotExist), "expected not-exist error, got %v", actErr)
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

// Test that a repeat call with an unchanged token does not rewrite the org file.
// The write is atomic (temp file + rename), so a rewrite replaces the file's inode;
// a skip leaves the same inode in place. os.SameFile compares device+inode.
func TestWriteOrgGitCreds_SkipsUnchanged(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := common.WriteOrgGitCreds("x-access-token", "token1", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)

	orgConfigFile := filepath.Join(tmp, ".gitconfig-org-my-org")
	before, err := os.Stat(orgConfigFile)
	Ok(t, err)

	// Identical inputs -> should skip the write and leave the file untouched.
	err = common.WriteOrgGitCreds("x-access-token", "token1", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)
	after, err := os.Stat(orgConfigFile)
	Ok(t, err)
	Assert(t, os.SameFile(before, after), "unchanged token should not rewrite the org config file")

	// A changed token must rewrite (new inode).
	err = common.WriteOrgGitCreds("x-access-token", "token2", "github.com", "my-org/", tmp, logger) // #nosec G101 -- test fixture
	Ok(t, err)
	changed, err := os.Stat(orgConfigFile)
	Ok(t, err)
	Assert(t, !os.SameFile(before, changed), "changed token should rewrite the org config file")
}

// Test that concurrent rotators writing shared git state never expose a partial/empty
// file to concurrent readers (the parallel-plan module-clone failure mode), and that
// there are no data races. Run with -race.
func TestGitCreds_ConcurrentNoPartialReads(t *testing.T) {
	logger := logging.NewNoopLogger(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	orgFileA := filepath.Join(tmp, ".gitconfig-org-org-a")
	orgFileB := filepath.Join(tmp, ".gitconfig-org-org-b")
	credsFile := filepath.Join(tmp, ".git-credentials")

	// isValidOrg returns true if the org file content is either absent/empty or a complete
	// rewrite block. A truncated/partial file (e.g. mid-write) would fail this check.
	isValidOrg := func(b []byte) bool {
		s := string(b)
		return s == "" || (containsStr(s, "[url \"https://") && containsStr(s, "insteadOf ="))
	}
	isValidCreds := func(b []byte) bool {
		s := string(b)
		return s == "" || containsStr(s, "https://x-access-token:")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Record partial-read failures here; assert on the test goroutine after Wait
	// (FailNow/Assert must not run in a spawned goroutine).
	var mu sync.Mutex
	var partials []string
	recordIfInvalid := func(path string, b []byte, valid func([]byte) bool) {
		if !valid(b) {
			mu.Lock()
			partials = append(partials, path+": "+string(b))
			mu.Unlock()
		}
	}

	// Writers: primary + two org rotators, looping like the 30s ticker would (but tight).
	writers := []func(i int){
		func(i int) {
			_ = common.WriteGitCreds("x-access-token", "tok-primary", "github.com", tmp, logger, true)
		},
		func(i int) {
			_ = common.WriteOrgGitCreds("x-access-token", "tok-a", "github.com", "org-a/", tmp, logger)
		},
		func(i int) {
			_ = common.WriteOrgGitCreds("x-access-token", "tok-b", "github.com", "org-b/", tmp, logger)
		},
	}
	for _, w := range writers {
		wg.Add(1)
		go func(write func(int)) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					write(i)
				}
			}
		}(w)
	}

	// Readers: emulate parallel `terraform init` clones reading git config.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					for path, valid := range map[string]func([]byte) bool{
						orgFileA: isValidOrg, orgFileB: isValidOrg, credsFile: isValidCreds,
					} {
						if b, err := os.ReadFile(path); err == nil {
							recordIfInvalid(path, b, valid)
						}
					}
				}
			}
		}()
	}

	// Let them interleave for a while, then stop.
	for i := 0; i < 2000; i++ {
		_, _ = os.ReadFile(orgFileA)
	}
	close(stop)
	wg.Wait()

	Assert(t, len(partials) == 0, "readers observed partial/invalid files: %v", partials)

	// Final state is complete and valid for every file.
	for _, f := range []string{orgFileA, orgFileB, credsFile} {
		b, err := os.ReadFile(f)
		Ok(t, err)
		Assert(t, len(b) > 0, "%s should be non-empty after writers finish", f)
	}
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
