// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/runatlantis/atlantis/server/logging"
	"github.com/runatlantis/atlantis/server/scheduled"
)

const (
	TokenEnvVar      = "VAULT_TOKEN"
	DefaultMountPath = "approle"

	rotationPeriod = time.Minute
	requestTimeout = 30 * time.Second
	minUsableTTL   = 5 * time.Minute
)

type AppRoleConfig struct {
	Address   string
	MountPath string
	RoleID    string
	SecretID  string
}

type AppRoleTokenRotator struct {
	log    logging.SimpleLogging
	cfg    AppRoleConfig
	client *http.Client
	now    func() time.Time

	token     string
	renewable bool
	issuedAt  time.Time
	lease     time.Duration
}

func NewAppRoleTokenRotator(log logging.SimpleLogging, cfg AppRoleConfig) *AppRoleTokenRotator {
	if cfg.MountPath == "" {
		cfg.MountPath = DefaultMountPath
	}
	cfg.Address = strings.TrimRight(cfg.Address, "/")
	cfg.MountPath = strings.Trim(cfg.MountPath, "/")
	return &AppRoleTokenRotator{
		log:    log,
		cfg:    cfg,
		client: &http.Client{Timeout: requestTimeout},
		now:    time.Now,
	}
}

var _ scheduled.Job = (*AppRoleTokenRotator)(nil)

func (r *AppRoleTokenRotator) GenerateJob() (scheduled.JobDefinition, error) {
	return scheduled.JobDefinition{
		Job:    r,
		Period: rotationPeriod,
	}, r.login()
}

func (r *AppRoleTokenRotator) Run() {
	if err := r.refresh(); err != nil {
		r.log.Err("refreshing vault token: %s", err)
	}
}

func (r *AppRoleTokenRotator) refresh() error {
	if r.token == "" {
		return r.login()
	}
	if r.lease == 0 || r.now().Before(r.issuedAt.Add(r.lease*2/3)) {
		return nil
	}
	if r.renewable {
		err := r.renew()
		if err == nil {
			return nil
		}
		r.log.Warn("renewing vault token, logging in again: %s", err)
	}
	return r.login()
}

func (r *AppRoleTokenRotator) login() error {
	body := map[string]string{"role_id": r.cfg.RoleID, "secret_id": r.cfg.SecretID}
	auth, err := r.post(fmt.Sprintf("/v1/auth/%s/login", r.cfg.MountPath), "", body)
	if err != nil {
		return fmt.Errorf("logging in to vault with approle: %w", err)
	}
	if err := r.setToken(auth); err != nil {
		return err
	}
	r.log.Info("logged in to vault with approle, token ttl %s, renewable %t", r.lease, r.renewable)
	return nil
}

func (r *AppRoleTokenRotator) renew() error {
	auth, err := r.post("/v1/auth/token/renew-self", r.token, map[string]string{})
	if err != nil {
		return err
	}
	if time.Duration(auth.LeaseDuration)*time.Second < minUsableTTL {
		return fmt.Errorf("renewed ttl %ds is below %s, token is near its max ttl", auth.LeaseDuration, minUsableTTL)
	}
	if err := r.setToken(auth); err != nil {
		return err
	}
	r.log.Debug("renewed vault token, ttl %s", r.lease)
	return nil
}

func (r *AppRoleTokenRotator) setToken(auth *authResponse) error {
	if auth.ClientToken == "" {
		return fmt.Errorf("vault response contained no client token")
	}
	if err := os.Setenv(TokenEnvVar, auth.ClientToken); err != nil {
		return fmt.Errorf("setting %s: %w", TokenEnvVar, err)
	}
	r.token = auth.ClientToken
	r.renewable = auth.Renewable
	r.lease = time.Duration(auth.LeaseDuration) * time.Second
	r.issuedAt = r.now()
	return nil
}

type authResponse struct {
	ClientToken   string `json:"client_token"`
	LeaseDuration int    `json:"lease_duration"`
	Renewable     bool   `json:"renewable"`
}

func (r *AppRoleTokenRotator) post(path string, token string, body any) (*authResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	endpoint, err := url.JoinPath(r.cfg.Address, path)
	if err != nil {
		return nil, fmt.Errorf("building vault url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", path, err)
	}

	var parsed struct {
		Auth   *authResponse `json:"auth"`
		Errors []string      `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil && resp.StatusCode == http.StatusOK {
		return nil, fmt.Errorf("decoding response from %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned status %d: %s", path, resp.StatusCode, strings.Join(parsed.Errors, "; "))
	}
	if parsed.Auth == nil {
		return nil, fmt.Errorf("%s response contained no auth block", path)
	}
	return parsed.Auth, nil
}
