// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package runtimebroker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ptone/scion-agent/pkg/runtime"
)

// newTestServerWithGrovePath creates a test server with a temporary grove path
// that has versioned settings with declared env vars.
func newTestServerWithGrovePath(t *testing.T, settingsYAML string) (*Server, *envCapturingManager, string) {
	t.Helper()

	// Create temp grove directory with settings
	// LoadEffectiveSettings expects a dir that contains settings.yaml directly
	groveDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(groveDir, "settings.yaml"), []byte(settingsYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultServerConfig()
	cfg.BrokerID = "test-broker-id"
	cfg.BrokerName = "test-host"
	cfg.Debug = true

	mgr := &envCapturingManager{}
	rt := &runtime.MockRuntime{}

	return New(cfg, mgr, rt), mgr, groveDir
}

// TestEnvGather_AllSatisfied tests the fast path: all required env keys are provided
// by the Hub and/or Broker, so the agent starts immediately (200/201).
func TestEnvGather_AllSatisfied(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    env:
      API_KEY: ""
profiles:
  default:
    runtime: docker
`
	srv, mgr, groveDir := newTestServerWithGrovePath(t, settings)

	body := `{
		"name": "test-agent",
		"id": "agent-uuid-123",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"resolvedEnv": {"API_KEY": "sk-test-key", "ANTHROPIC_API_KEY": "sk-ant-key"},
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Agent should have started with the key
	if mgr.lastEnv == nil {
		t.Fatal("expected env to be set")
	}
	if mgr.lastEnv["API_KEY"] != "sk-test-key" {
		t.Errorf("expected API_KEY='sk-test-key', got %q", mgr.lastEnv["API_KEY"])
	}
}

// TestEnvGather_NeedsKeys tests the gather path: required env keys are missing,
// so the broker returns 202 with requirements.
func TestEnvGather_NeedsKeys(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    env:
      API_KEY: ""
      SECRET_TOKEN: ""
profiles:
  default:
    runtime: docker
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	body := `{
		"name": "test-agent-gather",
		"id": "agent-uuid-456",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"resolvedEnv": {"API_KEY": "sk-from-hub"},
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if envReqs.AgentID != "agent-uuid-456" {
		t.Errorf("expected agentId='agent-uuid-456', got %q", envReqs.AgentID)
	}

	// API_KEY should be in hubHas
	found := false
	for _, k := range envReqs.HubHas {
		if k == "API_KEY" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected API_KEY in hubHas, got %v", envReqs.HubHas)
	}

	// SECRET_TOKEN should be in needs
	found = false
	for _, k := range envReqs.Needs {
		if k == "SECRET_TOKEN" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SECRET_TOKEN in needs, got %v", envReqs.Needs)
	}
}

// TestEnvGather_BrokerHasKey tests that the broker checks its own environment
// for missing keys before returning 202.
func TestEnvGather_BrokerHasKey(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    env:
      BROKER_LOCAL_KEY: ""
profiles:
  default:
    runtime: docker
`
	srv, mgr, groveDir := newTestServerWithGrovePath(t, settings)

	// Set the keys in the broker's own environment
	t.Setenv("BROKER_LOCAL_KEY", "broker-value")
	t.Setenv("ANTHROPIC_API_KEY", "broker-anthropic-key")

	body := `{
		"name": "test-agent-broker-env",
		"id": "agent-uuid-789",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// Should succeed (broker satisfies the key from its own env)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if mgr.lastEnv == nil {
		t.Fatal("expected env to be set")
	}
	if mgr.lastEnv["BROKER_LOCAL_KEY"] != "broker-value" {
		t.Errorf("expected BROKER_LOCAL_KEY='broker-value', got %q", mgr.lastEnv["BROKER_LOCAL_KEY"])
	}
}

// TestEnvGather_FinalizeEnv tests the finalize-env endpoint: after receiving
// a 202, the caller submits gathered env and the agent starts.
func TestEnvGather_FinalizeEnv(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    env:
      NEEDED_KEY: ""
profiles:
  default:
    runtime: docker
`
	srv, mgr, groveDir := newTestServerWithGrovePath(t, settings)

	// Phase 1: Create agent with gather — should get 202
	createBody := `{
		"name": "test-agent-finalize",
		"id": "agent-uuid-fin",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "claude", "profile": "default"}
	}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()

	srv.Handler().ServeHTTP(createW, createReq)

	if createW.Code != http.StatusAccepted {
		t.Fatalf("phase 1: expected 202, got %d: %s", createW.Code, createW.Body.String())
	}

	// Phase 2: Submit gathered env via finalize-env
	finalizeBody := `{"env": {"NEEDED_KEY": "gathered-value"}}`
	finalizeReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/test-agent-finalize/finalize-env", strings.NewReader(finalizeBody))
	finalizeReq.Header.Set("Content-Type", "application/json")
	finalizeW := httptest.NewRecorder()

	srv.Handler().ServeHTTP(finalizeW, finalizeReq)

	if finalizeW.Code != http.StatusCreated {
		t.Fatalf("phase 2: expected 201, got %d: %s", finalizeW.Code, finalizeW.Body.String())
	}

	// Verify agent was started with the gathered key
	if mgr.lastEnv == nil {
		t.Fatal("expected env to be set after finalize")
	}
	if mgr.lastEnv["NEEDED_KEY"] != "gathered-value" {
		t.Errorf("expected NEEDED_KEY='gathered-value', got %q", mgr.lastEnv["NEEDED_KEY"])
	}
}

// newTestServerWithHarnessConfig creates a test server with a temporary grove path
// that has a harness-config directory and optional settings YAML.
func newTestServerWithHarnessConfig(t *testing.T, harnessConfigName, configYAML, settingsYAML string) (*Server, *envCapturingManager, string) {
	t.Helper()

	groveDir := t.TempDir()

	// Create harness-configs/<name>/config.yaml
	hcDir := filepath.Join(groveDir, "harness-configs", harnessConfigName)
	if err := os.MkdirAll(hcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hcDir, "config.yaml"), []byte(configYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Write settings.yaml if provided
	if settingsYAML != "" {
		if err := os.WriteFile(filepath.Join(groveDir, "settings.yaml"), []byte(settingsYAML), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultServerConfig()
	cfg.BrokerID = "test-broker-id"
	cfg.BrokerName = "test-host"
	cfg.Debug = true

	mgr := &envCapturingManager{}
	rt := &runtime.MockRuntime{}

	return New(cfg, mgr, rt), mgr, groveDir
}

// TestEnvGather_HarnessAware tests that the broker detects required env keys
// from the harness itself, even when no harness_configs.env is declared in settings.
func TestEnvGather_HarnessAware(t *testing.T) {
	// No harness_configs in settings, but harness-config dir exists for "claude"
	srv, _, groveDir := newTestServerWithHarnessConfig(t, "claude",
		"harness: claude\nimage: test-image\nuser: scion\n",
		`
schema_version: "1"
profiles:
  default:
    runtime: docker
`)

	body := `{
		"name": "test-agent-harness-aware",
		"id": "agent-uuid-ha",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// Should return 202 because ANTHROPIC_API_KEY is needed but not provided
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	// ANTHROPIC_API_KEY should be in needs
	found := false
	for _, k := range envReqs.Needs {
		if k == "ANTHROPIC_API_KEY" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ANTHROPIC_API_KEY in needs, got needs=%v required=%v", envReqs.Needs, envReqs.Required)
	}
}

// TestEnvGather_GeminiAuthType tests that the gemini harness returns different
// required keys depending on auth_selected_type.
func TestEnvGather_GeminiAuthType(t *testing.T) {
	// Gemini with vertex-ai auth type → needs GOOGLE_CLOUD_PROJECT
	srv, _, groveDir := newTestServerWithHarnessConfig(t, "gemini",
		"harness: gemini\nimage: test-image\nuser: scion\nauth_selected_type: vertex-ai\n",
		`
schema_version: "1"
profiles:
  default:
    runtime: docker
`)

	body := `{
		"name": "test-agent-gemini-vertex",
		"id": "agent-uuid-gv",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "gemini", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	found := false
	for _, k := range envReqs.Needs {
		if k == "GOOGLE_CLOUD_PROJECT" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected GOOGLE_CLOUD_PROJECT in needs for vertex-ai auth, got needs=%v required=%v", envReqs.Needs, envReqs.Required)
	}
}

// TestEnvGather_SettingsAuthTypeOverride tests that a settings profile override
// for auth_selected_type takes precedence over the on-disk harness-config value.
func TestEnvGather_SettingsAuthTypeOverride(t *testing.T) {
	// On-disk config says gemini-api-key, but settings profile overrides to oauth-personal
	srv, mgr, groveDir := newTestServerWithHarnessConfig(t, "gemini",
		"harness: gemini\nimage: test-image\nuser: scion\nauth_selected_type: gemini-api-key\n",
		`
schema_version: "1"
profiles:
  default:
    runtime: docker
    harness_overrides:
      gemini:
        auth_selected_type: oauth-personal
`)

	body := `{
		"name": "test-agent-override",
		"id": "agent-uuid-ov",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "gemini", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// oauth-personal requires no env keys, so the agent should start immediately
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (oauth-personal needs no env), got %d: %s", w.Code, w.Body.String())
	}

	if mgr.lastEnv == nil {
		t.Fatal("expected env to be set")
	}
}

// TestEnvGather_NoGatherFlag tests that env-gather is skipped when GatherEnv is false.
func TestEnvGather_NoGatherFlag(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    env:
      MISSING_KEY: ""
profiles:
  default:
    runtime: docker
`
	srv, mgr, groveDir := newTestServerWithGrovePath(t, settings)

	body := `{
		"name": "test-agent-no-gather",
		"id": "agent-uuid-no-gather",
		"gatherEnv": false,
		"grovePath": "` + groveDir + `",
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// Should create the agent normally (201) even though env is missing
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Agent was started (env gather skipped)
	if mgr.lastEnv == nil {
		t.Fatal("expected env to be set")
	}
}

// TestEnvGather_SecretAutoUpgrade tests that when all required env keys are
// satisfied by resolved secrets, the env-gather check passes through without
// returning 202. The agent proceeds to creation (which may fail for other
// reasons in the test environment, but the key point is no 202 is returned).
func TestEnvGather_SecretAutoUpgrade(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    env:
      API_KEY: ""
profiles:
  default:
    runtime: docker
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	// Satisfy ANTHROPIC_API_KEY (from harness) via broker env
	t.Setenv("ANTHROPIC_API_KEY", "broker-ant-key")

	body := `{
		"name": "test-agent-secret-upgrade",
		"id": "agent-uuid-secret",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"resolvedSecrets": [
			{"name": "API_KEY", "type": "environment", "target": "API_KEY", "value": "secret-api-key", "source": "user"}
		],
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// Should NOT return 202 — all env keys are satisfied (API_KEY by secret,
	// ANTHROPIC_API_KEY by broker env). The request proceeds past env-gather.
	if w.Code == http.StatusAccepted {
		t.Fatalf("expected env-gather to pass (not 202), but got 202: %s", w.Body.String())
	}
}

// TestEnvGather_SecretPartialSatisfaction tests that when one required key is
// satisfied by a resolved secret but another is not, the broker returns 202
// with only the unsatisfied key in needs.
func TestEnvGather_SecretPartialSatisfaction(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    env:
      API_KEY: ""
      OTHER_TOKEN: ""
profiles:
  default:
    runtime: docker
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	body := `{
		"name": "test-agent-partial-secret",
		"id": "agent-uuid-partial",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"resolvedSecrets": [
			{"name": "API_KEY", "type": "environment", "target": "API_KEY", "value": "secret-api-key", "source": "user"}
		],
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// Should return 202 because OTHER_TOKEN is still unsatisfied
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	// API_KEY should be in hubHas (satisfied by secret)
	found := false
	for _, k := range envReqs.HubHas {
		if k == "API_KEY" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected API_KEY in hubHas, got %v", envReqs.HubHas)
	}

	// OTHER_TOKEN should be in needs
	found = false
	for _, k := range envReqs.Needs {
		if k == "OTHER_TOKEN" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected OTHER_TOKEN in needs, got %v", envReqs.Needs)
	}

	// API_KEY should NOT be in needs
	for _, k := range envReqs.Needs {
		if k == "API_KEY" {
			t.Error("API_KEY should not be in needs (satisfied by secret)")
		}
	}
}

// TestEnvGather_SettingsHarnessSecrets tests that secrets declared in
// harness_configs[*].secrets are extracted as required keys.
func TestEnvGather_SettingsHarnessSecrets(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    secrets:
      - key: THIRD_PARTY_TOKEN
        description: "Token for third-party API integration"
profiles:
  default:
    runtime: docker
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	body := `{
		"name": "test-agent-harness-secrets",
		"id": "agent-uuid-hs",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"resolvedEnv": {"ANTHROPIC_API_KEY": "sk-ant-key"},
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	// THIRD_PARTY_TOKEN should be in needs
	found := false
	for _, k := range envReqs.Needs {
		if k == "THIRD_PARTY_TOKEN" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected THIRD_PARTY_TOKEN in needs, got %v", envReqs.Needs)
	}

	// SecretInfo should be populated with description and source
	if envReqs.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be set")
	}
	info, ok := envReqs.SecretInfo["THIRD_PARTY_TOKEN"]
	if !ok {
		t.Fatal("expected THIRD_PARTY_TOKEN in SecretInfo")
	}
	if info.Description != "Token for third-party API integration" {
		t.Errorf("expected description='Token for third-party API integration', got %q", info.Description)
	}
	if info.Source != "settings" {
		t.Errorf("expected source='settings', got %q", info.Source)
	}
}

// TestEnvGather_SettingsProfileSecrets tests that secrets declared in
// profiles[*].secrets are extracted as required keys.
func TestEnvGather_SettingsProfileSecrets(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
profiles:
  default:
    runtime: docker
    secrets:
      - key: PROFILE_SECRET
        description: "Secret required by this profile"
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	// Satisfy harness key via broker env
	t.Setenv("ANTHROPIC_API_KEY", "broker-ant-key")

	body := `{
		"name": "test-agent-profile-secrets",
		"id": "agent-uuid-ps",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	// PROFILE_SECRET should be in needs
	found := false
	for _, k := range envReqs.Needs {
		if k == "PROFILE_SECRET" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PROFILE_SECRET in needs, got %v", envReqs.Needs)
	}
}

// TestEnvGather_RequestRequiredSecrets tests that RequiredSecrets in the
// create request (from Hub template) are extracted as required keys.
func TestEnvGather_RequestRequiredSecrets(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
profiles:
  default:
    runtime: docker
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	// Satisfy harness key via broker env
	t.Setenv("ANTHROPIC_API_KEY", "broker-ant-key")

	body := `{
		"name": "test-agent-req-secrets",
		"id": "agent-uuid-rs",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"requiredSecrets": [
			{"key": "HUB_TEMPLATE_KEY", "description": "Key from Hub template"}
		],
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	// HUB_TEMPLATE_KEY should be in needs
	found := false
	for _, k := range envReqs.Needs {
		if k == "HUB_TEMPLATE_KEY" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected HUB_TEMPLATE_KEY in needs, got %v", envReqs.Needs)
	}

	// SecretInfo should include the key with template source
	if envReqs.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be set")
	}
	info, ok := envReqs.SecretInfo["HUB_TEMPLATE_KEY"]
	if !ok {
		t.Fatal("expected HUB_TEMPLATE_KEY in SecretInfo")
	}
	if info.Description != "Key from Hub template" {
		t.Errorf("expected description='Key from Hub template', got %q", info.Description)
	}
	if info.Source != "template" {
		t.Errorf("expected source='template', got %q", info.Source)
	}
}

// TestEnvGather_SecretInfoOnlyNeeded tests that SecretInfo only includes
// keys that are in needs (not satisfied ones).
func TestEnvGather_SecretInfoOnlyNeeded(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    secrets:
      - key: SATISFIED_KEY
        description: "This key is satisfied"
      - key: MISSING_KEY
        description: "This key is missing"
profiles:
  default:
    runtime: docker
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	// Satisfy harness key and SATISFIED_KEY via resolved secrets
	body := `{
		"name": "test-agent-si-needed",
		"id": "agent-uuid-sin",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"resolvedEnv": {"ANTHROPIC_API_KEY": "sk-ant-key"},
		"resolvedSecrets": [
			{"name": "SATISFIED_KEY", "type": "environment", "target": "SATISFIED_KEY", "value": "satisfied-val", "source": "user"}
		],
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	// SecretInfo should include MISSING_KEY but NOT SATISFIED_KEY
	if envReqs.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be set")
	}
	if _, ok := envReqs.SecretInfo["SATISFIED_KEY"]; ok {
		t.Error("SecretInfo should NOT include satisfied keys")
	}
	if _, ok := envReqs.SecretInfo["MISSING_KEY"]; !ok {
		t.Error("SecretInfo should include MISSING_KEY")
	}
}

// TestEnvGather_SecretInfoIncludesType tests that the Type field from
// RequiredSecret declarations is propagated into SecretKeyInfo.
func TestEnvGather_SecretInfoIncludesType(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    secrets:
      - key: ENV_SECRET
        description: "An environment secret"
        type: environment
      - key: FILE_CERT
        description: "TLS certificate"
        type: file
profiles:
  default:
    runtime: docker
    secrets:
      - key: PROFILE_TOKEN
        description: "Profile token"
        type: variable
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	// Satisfy harness key via broker env
	t.Setenv("ANTHROPIC_API_KEY", "broker-ant-key")

	body := `{
		"name": "test-agent-type-prop",
		"id": "agent-uuid-tp",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if envReqs.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be set")
	}

	// Check ENV_SECRET has type "environment"
	if info, ok := envReqs.SecretInfo["ENV_SECRET"]; !ok {
		t.Error("expected ENV_SECRET in SecretInfo")
	} else if info.Type != "environment" {
		t.Errorf("expected ENV_SECRET type='environment', got %q", info.Type)
	}

	// Check FILE_CERT has type "file"
	if info, ok := envReqs.SecretInfo["FILE_CERT"]; !ok {
		t.Error("expected FILE_CERT in SecretInfo")
	} else if info.Type != "file" {
		t.Errorf("expected FILE_CERT type='file', got %q", info.Type)
	}

	// Check PROFILE_TOKEN has type "variable"
	if info, ok := envReqs.SecretInfo["PROFILE_TOKEN"]; !ok {
		t.Error("expected PROFILE_TOKEN in SecretInfo")
	} else if info.Type != "variable" {
		t.Errorf("expected PROFILE_TOKEN type='variable', got %q", info.Type)
	}

	// ANTHROPIC_API_KEY is a harness key — should have no type (empty string)
	if info, ok := envReqs.SecretInfo["ANTHROPIC_API_KEY"]; ok {
		// Harness keys are auto-added to SecretInfo but shouldn't appear in
		// the response if they're already satisfied (in hubHas/brokerHas).
		// If it does appear, type should be empty.
		if info.Type != "" {
			t.Errorf("expected ANTHROPIC_API_KEY type='', got %q", info.Type)
		}
	}
}

// TestEnvGather_SecretInfoIncludesType_Template tests that Type is populated
// from template RequiredSecrets in the create request.
func TestEnvGather_SecretInfoIncludesType_Template(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
profiles:
  default:
    runtime: docker
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	// Satisfy harness key via broker env
	t.Setenv("ANTHROPIC_API_KEY", "broker-ant-key")

	body := `{
		"name": "test-agent-type-tmpl",
		"id": "agent-uuid-tt",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"requiredSecrets": [
			{"key": "TMPL_FILE_SECRET", "description": "Template file secret", "type": "file"},
			{"key": "TMPL_ENV_SECRET", "description": "Template env secret", "type": "environment"}
		],
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if envReqs.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be set")
	}

	if info, ok := envReqs.SecretInfo["TMPL_FILE_SECRET"]; !ok {
		t.Error("expected TMPL_FILE_SECRET in SecretInfo")
	} else {
		if info.Type != "file" {
			t.Errorf("expected TMPL_FILE_SECRET type='file', got %q", info.Type)
		}
		if info.Source != "template" {
			t.Errorf("expected TMPL_FILE_SECRET source='template', got %q", info.Source)
		}
	}

	if info, ok := envReqs.SecretInfo["TMPL_ENV_SECRET"]; !ok {
		t.Error("expected TMPL_ENV_SECRET in SecretInfo")
	} else if info.Type != "environment" {
		t.Errorf("expected TMPL_ENV_SECRET type='environment', got %q", info.Type)
	}
}

// TestEnvGather_SettingsSecretsMerge tests that when the same key is declared
// in both harness config and profile, the profile description wins (most specific).
func TestEnvGather_SettingsSecretsMerge(t *testing.T) {
	settings := `
schema_version: "1"
harness_configs:
  claude:
    harness: claude
    secrets:
      - key: SHARED_KEY
        description: "From harness config"
profiles:
  default:
    runtime: docker
    secrets:
      - key: SHARED_KEY
        description: "From profile"
`
	srv, _, groveDir := newTestServerWithGrovePath(t, settings)

	// Satisfy harness key via broker env
	t.Setenv("ANTHROPIC_API_KEY", "broker-ant-key")

	body := `{
		"name": "test-agent-merge",
		"id": "agent-uuid-merge",
		"gatherEnv": true,
		"grovePath": "` + groveDir + `",
		"config": {"template": "claude", "profile": "default"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var envReqs EnvRequirementsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &envReqs); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if envReqs.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be set")
	}
	info, ok := envReqs.SecretInfo["SHARED_KEY"]
	if !ok {
		t.Fatal("expected SHARED_KEY in SecretInfo")
	}
	// Profile is processed after harness config, so profile description wins
	if info.Description != "From profile" {
		t.Errorf("expected description='From profile' (profile wins), got %q", info.Description)
	}
}
