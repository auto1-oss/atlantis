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

// gitConfigMu serializes all mutations of shared global git state (~/.gitconfig and the
// per-installation include files). The primary app rotator and every additional org
// rotator run in their own goroutine on a 30s ticker, all touching this shared state.
var gitConfigMu sync.Mutex

// atomicWriteFile writes data to path atomically (temp file in the same dir + rename), so a
// concurrent reader (e.g. parallel `terraform init` clones) never sees a partial file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
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

// ensureIncludePath adds configFile to ~/.gitconfig's include.path exactly once. After the
// first call it only reads config (no write), so there is no per-rotation churn. Callers
// must hold gitConfigMu.
func ensureIncludePath(configFile string, logger logging.SimpleLogging) error {
	getCmd := exec.Command("git", "config", "--global", "--get-all", "include.path") // nolint: gosec
	out, _ := getCmd.Output()
	if strings.Contains(string(out), configFile) {
		return nil
	}
	addCmd := exec.Command("git", "config", "--global", "--add", "include.path", configFile) // nolint: gosec
	if addOut, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("adding include.path for %s: %s: %w", configFile, string(addOut), err)
	}
	logger.Info("added include.path %s", configFile)
	return nil
}

// WriteAppGitCreds writes a catch-all git URL rewrite for the GitHub App's primary
// installation with the token embedded directly in the URL, included from ~/.gitconfig.
// Unlike WriteGitCreds it does NOT use `credential.helper store` / ~/.git-credentials:
// that mechanism keys credentials by host only, so an additional org's installation token
// can poison the single github.com credential slot and make same-org clones authenticate
// with the wrong token (HTTP 404). Embedding the token per rewrite, with git's longest-match
// rule, routes each org to its own token deterministically. Org-scoped rewrites written by
// WriteOrgGitCreds (e.g. github.com/wkda/) take precedence for their orgs.
func WriteAppGitCreds(gitUser, gitToken, gitHostname, home string, logger logging.SimpleLogging) error {
	gitConfigMu.Lock()
	defer gitConfigMu.Unlock()

	appConfigFile := filepath.Join(home, ".gitconfig-app")
	rewriteURL := fmt.Sprintf("https://%s:%s@%s/", gitUser, gitToken, gitHostname) // nolint: gosec
	content := fmt.Sprintf("[url %q]\n\tinsteadOf = ssh://git@%s/\n\tinsteadOf = https://%s/\n",
		rewriteURL, gitHostname, gitHostname)

	if existing, err := os.ReadFile(appConfigFile); err == nil && string(existing) == content {
		logger.Debug("app git credentials unchanged, not modifying")
		return ensureIncludePath(appConfigFile, logger)
	}
	if err := atomicWriteFile(appConfigFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing app git config: %w", err)
	}
	logger.Debug("refreshed app git credentials %s", appConfigFile)
	return ensureIncludePath(appConfigFile, logger)
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

	// Skip when unchanged (token typically unchanged between 30s rotations) to avoid churn.
	if existing, err := os.ReadFile(orgConfigFile); err == nil && string(existing) == content {
		logger.Debug("org git credentials for %s unchanged, not modifying", orgConfigFile)
		return ensureIncludePath(orgConfigFile, logger)
	}
	if err := atomicWriteFile(orgConfigFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing org git config for %s: %w", orgName, err)
	}
	logger.Debug("refreshed org git credentials for %s", orgConfigFile)
	return ensureIncludePath(orgConfigFile, logger)
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
