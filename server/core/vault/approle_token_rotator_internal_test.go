// Copyright 2025 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package vault

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/runatlantis/atlantis/server/logging"
	. "github.com/runatlantis/atlantis/testing"
)

type fakeVault struct {
	logins       int
	renewals     int
	loginTTL     int
	renewTTL     int
	renewable    bool
	failRenew    bool
	failLogin    bool
	lastRenewTok string
	lastLogin    map[string]string
}

func (f *fakeVault) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/approle/login":
			if f.failLogin {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"errors":["invalid role or secret ID"]}`)
				return
			}
			Ok(t, json.NewDecoder(r.Body).Decode(&f.lastLogin))
			f.logins++
			writeAuth(w, fmt.Sprintf("token-%d", f.logins), f.loginTTL, f.renewable)
		case "/v1/auth/token/renew-self":
			f.renewals++
			f.lastRenewTok = r.Header.Get("X-Vault-Token")
			if f.failRenew {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"errors":["permission denied"]}`)
				return
			}
			writeAuth(w, f.lastRenewTok, f.renewTTL, f.renewable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func writeAuth(w http.ResponseWriter, token string, ttl int, renewable bool) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"auth": map[string]any{"client_token": token, "lease_duration": ttl, "renewable": renewable},
	})
}

func newTestRotator(t *testing.T, fv *fakeVault) (*AppRoleTokenRotator, *time.Time) {
	srv := httptest.NewServer(fv.handler(t))
	t.Cleanup(srv.Close)
	t.Setenv(TokenEnvVar, "static-token")
	r := NewAppRoleTokenRotator(logging.NewNoopLogger(t), AppRoleConfig{
		Address:  srv.URL + "/",
		RoleID:   "role",
		SecretID: "secret",
	})
	now := time.Unix(1_700_000_000, 0)
	r.now = func() time.Time { return now }
	return r, &now
}

func TestGenerateJob_LogsInAndExportsToken(t *testing.T) {
	fv := &fakeVault{loginTTL: 3600, renewable: true}
	r, _ := newTestRotator(t, fv)

	jd, err := r.GenerateJob()
	Ok(t, err)
	Equals(t, rotationPeriod, jd.Period)
	Equals(t, 1, fv.logins)
	Equals(t, map[string]string{"role_id": "role", "secret_id": "secret"}, fv.lastLogin)
	Equals(t, "token-1", os.Getenv(TokenEnvVar))
}

func TestGenerateJob_LoginFailureReturnsError(t *testing.T) {
	fv := &fakeVault{failLogin: true}
	r, _ := newTestRotator(t, fv)

	_, err := r.GenerateJob()
	ErrContains(t, "invalid role or secret ID", err)
	Equals(t, "static-token", os.Getenv(TokenEnvVar))
}

func TestRun_NoopBeforeRefreshWindow(t *testing.T) {
	fv := &fakeVault{loginTTL: 3600, renewTTL: 3600, renewable: true}
	r, now := newTestRotator(t, fv)
	_, err := r.GenerateJob()
	Ok(t, err)

	*now = now.Add(39 * time.Minute)
	r.Run()
	Equals(t, 1, fv.logins)
	Equals(t, 0, fv.renewals)
}

func TestRun_RenewsSameTokenInRefreshWindow(t *testing.T) {
	fv := &fakeVault{loginTTL: 3600, renewTTL: 3600, renewable: true}
	r, now := newTestRotator(t, fv)
	_, err := r.GenerateJob()
	Ok(t, err)

	*now = now.Add(41 * time.Minute)
	r.Run()
	Equals(t, 1, fv.logins)
	Equals(t, 1, fv.renewals)
	Equals(t, "token-1", fv.lastRenewTok)
	Equals(t, "token-1", os.Getenv(TokenEnvVar))

	*now = now.Add(39 * time.Minute)
	r.Run()
	Equals(t, 1, fv.renewals)
}

func TestRun_LogsInAgainWhenRenewFails(t *testing.T) {
	fv := &fakeVault{loginTTL: 3600, renewable: true, failRenew: true}
	r, now := newTestRotator(t, fv)
	_, err := r.GenerateJob()
	Ok(t, err)

	*now = now.Add(41 * time.Minute)
	r.Run()
	Equals(t, 1, fv.renewals)
	Equals(t, 2, fv.logins)
	Equals(t, "token-2", os.Getenv(TokenEnvVar))
}

func TestRun_LogsInAgainNearMaxTTL(t *testing.T) {
	fv := &fakeVault{loginTTL: 3600, renewTTL: 120, renewable: true}
	r, now := newTestRotator(t, fv)
	_, err := r.GenerateJob()
	Ok(t, err)

	*now = now.Add(41 * time.Minute)
	r.Run()
	Equals(t, 1, fv.renewals)
	Equals(t, 2, fv.logins)
	Equals(t, "token-2", os.Getenv(TokenEnvVar))
}

func TestRun_NonRenewableTokenLogsInAgain(t *testing.T) {
	fv := &fakeVault{loginTTL: 3600, renewable: false}
	r, now := newTestRotator(t, fv)
	_, err := r.GenerateJob()
	Ok(t, err)

	*now = now.Add(41 * time.Minute)
	r.Run()
	Equals(t, 0, fv.renewals)
	Equals(t, 2, fv.logins)
}

func TestRun_KeepsCurrentTokenWhenVaultUnavailable(t *testing.T) {
	fv := &fakeVault{loginTTL: 3600, renewable: true, failRenew: true}
	r, now := newTestRotator(t, fv)
	_, err := r.GenerateJob()
	Ok(t, err)

	fv.failLogin = true
	*now = now.Add(41 * time.Minute)
	r.Run()
	Equals(t, "token-1", os.Getenv(TokenEnvVar))

	fv.failLogin = false
	*now = now.Add(time.Minute)
	r.Run()
	Equals(t, "token-2", os.Getenv(TokenEnvVar))
}

func TestRun_NonExpiringTokenIsNeverRefreshed(t *testing.T) {
	fv := &fakeVault{loginTTL: 0, renewable: false}
	r, now := newTestRotator(t, fv)
	_, err := r.GenerateJob()
	Ok(t, err)

	*now = now.Add(24 * time.Hour)
	r.Run()
	Equals(t, 1, fv.logins)
}

func TestNewAppRoleTokenRotator_CustomMountPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeAuth(w, "tok", 3600, true)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(TokenEnvVar, "")

	r := NewAppRoleTokenRotator(logging.NewNoopLogger(t), AppRoleConfig{
		Address:   srv.URL,
		MountPath: "/atlantis-approle/",
		RoleID:    "role",
		SecretID:  "secret",
	})
	_, err := r.GenerateJob()
	Ok(t, err)
	Equals(t, "/v1/auth/atlantis-approle/login", gotPath)
}
