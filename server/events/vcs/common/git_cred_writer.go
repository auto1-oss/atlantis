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

	"github.com/runatlantis/atlantis/server/logging"
)

// WriteGitCreds generates a .git-credentials file containing the username and token
// used for authenticating with git over HTTPS
// It will create the file in home/.git-credentials
// If ghAccessToken is true we will look for a line starting with https://x-access-token and ending with gitHostname and replace it.
func WriteGitCreds(gitUser string, gitToken string, gitHostname string, home string, logger logging.SimpleLogging, ghAccessToken bool) error {
	const credsFilename = ".git-credentials"
	credsFile := filepath.Join(home, credsFilename)
	credsFileContentsPattern := `https://%s:%s@%s` // nolint: gosec
	config := fmt.Sprintf(credsFileContentsPattern, gitUser, gitToken, gitHostname)

	// If the file doesn't exist, write it.
	if _, err := os.Stat(credsFile); err != nil {
		if err := os.WriteFile(credsFile, []byte(config), 0600); err != nil {
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
	orgName := strings.TrimSuffix(orgPath, "/")
	orgConfigFile := filepath.Join(home, fmt.Sprintf(".gitconfig-org-%s", orgName))

	// Embed the token directly in the rewrite URL so no .git-credentials lookup is needed.
	rewriteURL := fmt.Sprintf("https://%s:%s@%s/%s", gitUser, gitToken, gitHostname, orgPath) // nolint: gosec
	content := fmt.Sprintf("[url %q]\n\tinsteadOf = ssh://git@%s/%s\n\tinsteadOf = https://%s/%s\n",
		rewriteURL, gitHostname, orgPath, gitHostname, orgPath)

	if err := os.WriteFile(orgConfigFile, []byte(content), 0600); err != nil {
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
	return os.WriteFile(filename, []byte(string(currContents)+line), 0600)
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

	return os.WriteFile(filename, []byte(toWrite), 0600)
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
