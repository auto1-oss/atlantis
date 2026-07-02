// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package common

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/runatlantis/atlantis/server/logging"
)

// gitConfigMu serializes all mutations of shared global git state (~/.gitconfig,
// ~/.git-credentials, and per-org include files). The primary token rotator and every
// additional org rotator run in their own goroutine on a 30s ticker (see
// scheduled.ExecutorService), and all invoke WriteGitCreds/WriteOrgGitCreds which run
// `git config --global ...` against the same ~/.gitconfig. Without this lock, concurrent
// `git config` invocations contend on ~/.gitconfig.lock (dropping include.path entries) and
// the ~/.git-credentials read-modify-write races, which intermittently leaves the wrong
// installation token active for a clone.
var gitConfigMu sync.Mutex

// atomicWriteFile writes data to path atomically by writing to a temp file in the same
// directory and renaming it into place. Concurrent readers (e.g. parallel `terraform init`
// module clones reading git config) therefore always observe either the complete old file
// or the complete new file, never a truncated/partial write.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp file %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}

// WriteGitCreds generates a .git-credentials file containing the username and token
// used for authenticating with git over HTTPS
// It will create the file in home/.git-credentials
// If ghAccessToken is true we will look for a line starting with https://x-access-token and ending with gitHostname and replace it.
func WriteGitCreds(gitUser string, gitToken string, gitHostname string, home string, logger logging.SimpleLogging, ghAccessToken bool) error {
	gitConfigMu.Lock()
	defer gitConfigMu.Unlock()

	const credsFilename = ".git-credentials"
	credsFile := filepath.Join(home, credsFilename)
	credsFileContentsPattern := `https://%s:%s@%s` // nolint: gosec
	config := fmt.Sprintf(credsFileContentsPattern, gitUser, gitToken, gitHostname)

	// If the file doesn't exist, write it.
	if _, err := os.Stat(credsFile); err != nil {
		if err := atomicWriteFile(credsFile, []byte(config), 0600); err != nil {
			return fmt.Errorf("writing generated %s file with user, token and hostname to %s: %w", credsFilename, credsFile, err)
		}
		logger.Info("wrote git credentials to %s", credsFile)
	} else {
		hasLine, err := fileHasLine(config, credsFile)
		if err != nil {
			return err
		}
		if hasLine {
			logger.Debug("git credentials file has expected contents, not modifying")
			return nil
		}

		if ghAccessToken {
			hasGHToken, err := fileHasGHToken(gitUser, gitHostname, credsFile)
			if err != nil {
				return err
			}
			if hasGHToken {
				// Need to replace the line.
				if err := fileLineReplace(config, gitUser, gitHostname, credsFile); err != nil {
					return fmt.Errorf("replacing git credentials line for github app: %w", err)
				}
				logger.Info("updated git credentials in %s", credsFile)
			} else {
				if err := fileAppend(config, credsFile); err != nil {
					return err
				}
				logger.Info("wrote git credentials to %s", credsFile)
			}

		} else {
			// Otherwise we need to append the line.
			if err := fileAppend(config, credsFile); err != nil {
				return err
			}
			logger.Info("wrote git credentials to %s", credsFile)
		}
	}

	credentialCmd := exec.Command("git", "config", "--global", "credential.helper", "store")
	if out, err := credentialCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("running %s: %s: %w", strings.Join(credentialCmd.Args, " "), string(out), err)
	}
	logger.Info("successfully ran %s", strings.Join(credentialCmd.Args, " "))

	urlCmd := exec.Command("git", "config", "--global", fmt.Sprintf("url.https://%s@%s.insteadOf", gitUser, gitHostname), fmt.Sprintf("ssh://git@%s", gitHostname)) // nolint: gosec
	if out, err := urlCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("running %s: %s: %w", strings.Join(urlCmd.Args, " "), string(out), err)
	}
	logger.Info("successfully ran %s", strings.Join(urlCmd.Args, " "))
	return nil
}

// WriteOrgGitCreds writes org-specific git URL rewrites with the installation token embedded
// directly in the URL. This allows a single GitHub App with multiple installations to provide
// scoped access per org. The rewrites are written to a dedicated per-org config file and
// included from ~/.gitconfig via an [include] directive.
// orgPath is the GitHub org name with trailing slash, e.g. "wkda/".
func WriteOrgGitCreds(gitUser, gitToken, gitHostname, orgPath, home string, logger logging.SimpleLogging) error {
	gitConfigMu.Lock()
	defer gitConfigMu.Unlock()

	orgName := strings.TrimSuffix(orgPath, "/")
	orgConfigFile := filepath.Join(home, fmt.Sprintf(".gitconfig-org-%s", orgName))

	// Embed the token directly in the rewrite URL so no .git-credentials lookup is needed.
	rewriteURL := fmt.Sprintf("https://%s:%s@%s/%s", gitUser, gitToken, gitHostname, orgPath) // nolint: gosec
	content := fmt.Sprintf("[url %q]\n\tinsteadOf = ssh://git@%s/%s\n\tinsteadOf = https://%s/%s\n",
		rewriteURL, gitHostname, orgPath, gitHostname, orgPath)

	// Skip the rewrite when nothing changed (the installation token is typically unchanged
	// between 30s rotations), to avoid needless churn on the shared config file.
	if existing, err := os.ReadFile(orgConfigFile); err == nil && string(existing) == content {
		logger.Debug("org git credentials for %s unchanged, not modifying", orgConfigFile)
		return nil
	}

	if err := atomicWriteFile(orgConfigFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing org git config for %s: %w", orgName, err)
	}
	logger.Debug("refreshed org git credentials for %s", orgConfigFile)

	// Ensure ~/.gitconfig includes the org config file (add once).
	getCmd := exec.Command("git", "config", "--global", "--get-all", "include.path") // nolint: gosec
	out, _ := getCmd.Output()
	if !strings.Contains(string(out), orgConfigFile) {
		addCmd := exec.Command("git", "config", "--global", "--add", "include.path", orgConfigFile) // nolint: gosec
		if addOut, err := addCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("adding include.path for %s: %s: %w", orgConfigFile, string(addOut), err)
		}
		logger.Info("added include.path for org %s (%s)", orgName, orgConfigFile)
	}

	return nil
}

func fileHasLine(line string, filename string) (bool, error) {
	currContents, err := os.ReadFile(filename) // nolint: gosec
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", filename, err)
	}
	return slices.Contains(strings.Split(string(currContents), "\n"), line), nil
}

func fileAppend(line string, filename string) error {
	currContents, err := os.ReadFile(filename) // nolint: gosec
	if err != nil {
		return err
	}
	if len(currContents) > 0 && !strings.HasSuffix(string(currContents), "\n") {
		line = "\n" + line
	}
	return atomicWriteFile(filename, []byte(string(currContents)+line), 0600) // #nosec G703 -- filename comes from trusted caller-supplied $HOME path
}

func fileLineReplace(line, user, host, filename string) error {
	currContents, err := os.ReadFile(filename) // nolint: gosec
	if err != nil {
		return err
	}
	prevLines := strings.Split(string(currContents), "\n")
	var newLines []string
	for _, l := range prevLines {
		if strings.HasPrefix(l, "https://"+user) && strings.HasSuffix(l, host) {
			newLines = append(newLines, line)
		} else {
			newLines = append(newLines, l)
		}
	}
	toWrite := strings.Join(newLines, "\n")

	// there was nothing to replace so we need to append the creds
	if toWrite == "" {
		return fileAppend(line, filename)
	}

	return atomicWriteFile(filename, []byte(toWrite), 0600) // #nosec G703 -- filename comes from trusted caller-supplied $HOME path
}

func fileHasGHToken(user, host, filename string) (bool, error) {
	currContents, err := os.ReadFile(filename) // nolint: gosec
	if err != nil {
		return false, err
	}
	for l := range strings.SplitSeq(string(currContents), "\n") {
		if strings.HasPrefix(l, "https://"+user) && strings.HasSuffix(l, host) {
			return true, nil
		}
	}
	return false, nil
}
