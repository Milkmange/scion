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

//go:build !no_sqlite

package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/ptone/scion-agent/pkg/secret"
	"github.com/ptone/scion-agent/pkg/store"
)

// envGatherMockBrokerClient extends mockRuntimeBrokerClient with env-gather methods.
type envGatherMockBrokerClient struct {
	mockRuntimeBrokerClient

	// Env-gather fields
	createWithGatherCalled bool
	finalizeCalled         bool
	gatherReturnEnvReqs    *RemoteEnvRequirementsResponse
	lastFinalizeAgentID    string
	lastFinalizeEnv        map[string]string
}

func (m *envGatherMockBrokerClient) CreateAgentWithGather(ctx context.Context, brokerID, brokerEndpoint string, req *RemoteCreateAgentRequest) (*RemoteAgentResponse, *RemoteEnvRequirementsResponse, error) {
	m.createWithGatherCalled = true
	m.lastBrokerID = brokerID
	m.lastEndpoint = brokerEndpoint
	m.lastCreateReq = req
	if m.returnErr != nil {
		return nil, nil, m.returnErr
	}
	if m.gatherReturnEnvReqs != nil {
		return nil, m.gatherReturnEnvReqs, nil
	}
	// All env satisfied
	return &RemoteAgentResponse{
		Agent: &RemoteAgentInfo{
			ID:     req.ID,
			Slug:   req.Slug,
			Name:   req.Name,
			Status: "running",
		},
		Created: true,
	}, nil, nil
}

func (m *envGatherMockBrokerClient) FinalizeEnv(ctx context.Context, brokerID, brokerEndpoint, agentID string, env map[string]string) (*RemoteAgentResponse, error) {
	m.finalizeCalled = true
	m.lastFinalizeAgentID = agentID
	m.lastFinalizeEnv = env
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	return &RemoteAgentResponse{
		Agent: &RemoteAgentInfo{
			ID:     agentID,
			Name:   agentID,
			Status: "running",
		},
		Created: true,
	}, nil
}

// TestEnvGather_HubDispatch_AllSatisfied tests that when env-gather is enabled
// and all env vars are satisfied, the agent starts normally.
func TestEnvGather_HubDispatch_AllSatisfied(t *testing.T) {
	ctx := context.Background()
	memStore := createTestStore(t)

	broker := &store.RuntimeBroker{
		ID:       "broker-1",
		Name:     "test-broker",
		Slug:     "test-broker",
		Endpoint: "http://localhost:9800",
		Status:   store.BrokerStatusOnline,
	}
	if err := memStore.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	grove := &store.Grove{
		ID:   "grove-1",
		Name: "test-grove",
		Slug: "test-grove",
	}
	if err := memStore.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	// Add provider so broker can serve this grove
	if err := memStore.AddGroveProvider(ctx, &store.GroveProvider{
		GroveID:  "grove-1",
		BrokerID: "broker-1",
	}); err != nil {
		t.Fatal(err)
	}

	mockClient := &envGatherMockBrokerClient{}
	dispatcher := NewHTTPAgentDispatcherWithClient(memStore, mockClient, true)

	agent := &store.Agent{
		ID:              "agent-1",
		Name:            "test-agent",
		Slug:            "test-agent",
		GroveID:         "grove-1",
		RuntimeBrokerID: "broker-1",
		AppliedConfig: &store.AgentAppliedConfig{
			Harness: "claude",
		},
	}

	envReqs, err := dispatcher.DispatchAgentCreateWithGather(ctx, agent)
	if err != nil {
		t.Fatalf("DispatchAgentCreateWithGather failed: %v", err)
	}

	if envReqs != nil {
		t.Errorf("expected nil env requirements (all satisfied), got %+v", envReqs)
	}

	if !mockClient.createWithGatherCalled {
		t.Error("expected CreateAgentWithGather to be called")
	}

	// Request should have GatherEnv set
	if mockClient.lastCreateReq != nil && !mockClient.lastCreateReq.GatherEnv {
		t.Error("expected GatherEnv=true in request")
	}
}

// TestEnvGather_HubDispatch_NeedsGather tests that when the broker returns 202
// with env requirements, the dispatcher relays them correctly.
func TestEnvGather_HubDispatch_NeedsGather(t *testing.T) {
	ctx := context.Background()
	memStore := createTestStore(t)

	broker := &store.RuntimeBroker{
		ID:       "broker-2",
		Name:     "test-broker-2",
		Slug:     "test-broker-2",
		Endpoint: "http://localhost:9800",
		Status:   store.BrokerStatusOnline,
	}
	if err := memStore.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	mockClient := &envGatherMockBrokerClient{
		gatherReturnEnvReqs: &RemoteEnvRequirementsResponse{
			AgentID:  "agent-2",
			Required: []string{"API_KEY", "SECRET"},
			HubHas:   []string{"API_KEY"},
			Needs:    []string{"SECRET"},
		},
	}
	dispatcher := NewHTTPAgentDispatcherWithClient(memStore, mockClient, true)

	agent := &store.Agent{
		ID:              "agent-2",
		Name:            "test-agent-2",
		Slug:            "test-agent-2",
		GroveID:         "grove-1",
		RuntimeBrokerID: "broker-2",
		AppliedConfig: &store.AgentAppliedConfig{
			Harness: "claude",
		},
	}

	envReqs, err := dispatcher.DispatchAgentCreateWithGather(ctx, agent)
	if err != nil {
		t.Fatalf("DispatchAgentCreateWithGather failed: %v", err)
	}

	if envReqs == nil {
		t.Fatal("expected env requirements, got nil")
	}

	if len(envReqs.Needs) != 1 || envReqs.Needs[0] != "SECRET" {
		t.Errorf("expected needs=[SECRET], got %v", envReqs.Needs)
	}
	if len(envReqs.HubHas) != 1 || envReqs.HubHas[0] != "API_KEY" {
		t.Errorf("expected hubHas=[API_KEY], got %v", envReqs.HubHas)
	}
}

// TestEnvGather_HubDispatch_FinalizeEnv tests that DispatchFinalizeEnv properly
// sends gathered env to the broker.
func TestEnvGather_HubDispatch_FinalizeEnv(t *testing.T) {
	ctx := context.Background()
	memStore := createTestStore(t)

	broker := &store.RuntimeBroker{
		ID:       "broker-3",
		Name:     "test-broker-3",
		Slug:     "test-broker-3",
		Endpoint: "http://localhost:9800",
		Status:   store.BrokerStatusOnline,
	}
	if err := memStore.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	mockClient := &envGatherMockBrokerClient{}
	dispatcher := NewHTTPAgentDispatcherWithClient(memStore, mockClient, true)

	agent := &store.Agent{
		ID:              "agent-3",
		Name:            "test-agent-3",
		Slug:            "test-agent-3",
		GroveID:         "grove-1",
		RuntimeBrokerID: "broker-3",
	}

	gatheredEnv := map[string]string{
		"SECRET":  "gathered-secret-value",
		"API_KEY": "gathered-api-key",
	}

	err := dispatcher.DispatchFinalizeEnv(ctx, agent, gatheredEnv)
	if err != nil {
		t.Fatalf("DispatchFinalizeEnv failed: %v", err)
	}

	if !mockClient.finalizeCalled {
		t.Error("expected FinalizeEnv to be called")
	}
	if mockClient.lastFinalizeEnv["SECRET"] != "gathered-secret-value" {
		t.Errorf("expected SECRET=gathered-secret-value, got %q", mockClient.lastFinalizeEnv["SECRET"])
	}
}

// TestEnvGather_HubHandler_202Response tests the full Hub handler flow:
// when GatherEnv is true and the broker returns 202, the Hub returns 202
// to the CLI with EnvGather response.
func TestEnvGather_HubHandler_202Response(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	// Create grove
	grove := &store.Grove{ID: "grove-gather", Name: "gather-grove", Slug: "gather-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	// Create broker
	broker := &store.RuntimeBroker{
		ID: "broker-gather", Name: "gather-broker", Slug: "gather-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := st.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	// Add provider with local path so template can be resolved locally
	if err := st.AddGroveProvider(ctx, &store.GroveProvider{
		GroveID: "grove-gather", BrokerID: "broker-gather",
		LocalPath: "/tmp/test-grove",
	}); err != nil {
		t.Fatal(err)
	}

	// Set up dispatcher with mock that returns env requirements
	mockClient := &envGatherMockBrokerClient{
		gatherReturnEnvReqs: &RemoteEnvRequirementsResponse{
			AgentID:  "will-be-set",
			Required: []string{"GEMINI_API_KEY"},
			Needs:    []string{"GEMINI_API_KEY"},
		},
	}
	dispatcher := NewHTTPAgentDispatcherWithClient(st, mockClient, true)
	srv.SetDispatcher(dispatcher)

	// Create agent with GatherEnv=true
	reqBody := map[string]interface{}{
		"name":     "gather-agent",
		"groveId":  "grove-gather",
		"template": "claude",
		"gatherEnv": true,
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/agents", reqBody)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CreateAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if resp.EnvGather == nil {
		t.Fatal("expected EnvGather to be set in response")
	}

	if len(resp.EnvGather.Needs) != 1 || resp.EnvGather.Needs[0] != "GEMINI_API_KEY" {
		t.Errorf("expected needs=[GEMINI_API_KEY], got %v", resp.EnvGather.Needs)
	}

	// Agent should be in provisioning status
	if resp.Agent == nil {
		t.Fatal("expected agent in response")
	}
	if resp.Agent.Status != store.AgentStatusProvisioning {
		t.Errorf("expected agent status=%q, got %q", store.AgentStatusProvisioning, resp.Agent.Status)
	}
}

// TestEnvGather_HubHandler_GroveRoute_202Response tests env-gather via the
// grove-scoped route /api/v1/groves/{groveId}/agents which is the path the CLI uses.
func TestEnvGather_HubHandler_GroveRoute_202Response(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	// Create grove
	grove := &store.Grove{ID: "grove-gather-route", Name: "gather-route-grove", Slug: "gather-route-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	// Create broker
	broker := &store.RuntimeBroker{
		ID: "broker-gather-route", Name: "gather-route-broker", Slug: "gather-route-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := st.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	// Add provider with local path so template can be resolved locally
	if err := st.AddGroveProvider(ctx, &store.GroveProvider{
		GroveID: "grove-gather-route", BrokerID: "broker-gather-route",
		LocalPath: "/tmp/test-grove",
	}); err != nil {
		t.Fatal(err)
	}

	// Set up dispatcher with mock that returns env requirements
	mockClient := &envGatherMockBrokerClient{
		gatherReturnEnvReqs: &RemoteEnvRequirementsResponse{
			AgentID:  "will-be-set",
			Required: []string{"GEMINI_API_KEY"},
			Needs:    []string{"GEMINI_API_KEY"},
		},
	}
	dispatcher := NewHTTPAgentDispatcherWithClient(st, mockClient, true)
	srv.SetDispatcher(dispatcher)

	// Create agent via grove-scoped route with GatherEnv=true
	reqBody := map[string]interface{}{
		"name":      "gather-route-agent",
		"template":  "claude",
		"gatherEnv": true,
	}

	rec := doRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/groves/%s/agents", grove.ID), reqBody)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CreateAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if resp.EnvGather == nil {
		t.Fatal("expected EnvGather to be set in response")
	}

	if len(resp.EnvGather.Needs) != 1 || resp.EnvGather.Needs[0] != "GEMINI_API_KEY" {
		t.Errorf("expected needs=[GEMINI_API_KEY], got %v", resp.EnvGather.Needs)
	}

	// Agent should be in provisioning status
	if resp.Agent == nil {
		t.Fatal("expected agent in response")
	}
	if resp.Agent.Status != store.AgentStatusProvisioning {
		t.Errorf("expected agent status=%q, got %q", store.AgentStatusProvisioning, resp.Agent.Status)
	}

	// Verify the dispatcher was called with gather (not regular create)
	if !mockClient.createWithGatherCalled {
		t.Error("expected CreateAgentWithGather to be called, but it wasn't")
	}
}

// TestEnvGather_HubHandler_SubmitEnv tests the env submission endpoint.
func TestEnvGather_HubHandler_SubmitEnv(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	// Create grove
	grove := &store.Grove{ID: "grove-submit", Name: "submit-grove", Slug: "submit-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	// Create broker
	broker := &store.RuntimeBroker{
		ID: "broker-submit", Name: "submit-broker", Slug: "submit-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := st.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	// Create agent in provisioning state (as if 202 was already returned)
	agent := &store.Agent{
		ID:              "agent-submit",
		Name:            "submit-agent",
		Slug:            "submit-agent",
		GroveID:         "grove-submit",
		RuntimeBrokerID: "broker-submit",
		Status:          store.AgentStatusProvisioning,
		AppliedConfig: &store.AgentAppliedConfig{
			Harness: "claude",
		},
	}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}

	// Set up dispatcher
	mockClient := &envGatherMockBrokerClient{}
	dispatcher := NewHTTPAgentDispatcherWithClient(st, mockClient, true)
	srv.SetDispatcher(dispatcher)

	// Submit env
	reqBody := map[string]interface{}{
		"env": map[string]string{
			"GEMINI_API_KEY": "test-api-key-value",
		},
	}

	path := fmt.Sprintf("/api/v1/groves/grove-submit/agents/submit-agent/env")
	rec := doRequest(t, srv, http.MethodPost, path, reqBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !mockClient.finalizeCalled {
		t.Error("expected FinalizeEnv to be called on broker")
	}

	if mockClient.lastFinalizeEnv["GEMINI_API_KEY"] != "test-api-key-value" {
		t.Errorf("expected GEMINI_API_KEY=test-api-key-value, got %q", mockClient.lastFinalizeEnv["GEMINI_API_KEY"])
	}

	// Agent should be updated to running
	updated, err := st.GetAgent(ctx, "agent-submit")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != store.AgentStatusRunning {
		t.Errorf("expected agent status=running, got %q", updated.Status)
	}
}

// TestEnvGather_HubHandler_SubmitEnv_InvalidState tests that env submission
// is rejected when the agent is not in a valid state.
func TestEnvGather_HubHandler_SubmitEnv_InvalidState(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	// Create grove
	grove := &store.Grove{ID: "grove-invalid", Name: "invalid-grove", Slug: "invalid-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	// Create agent in running state (not valid for env submission)
	agent := &store.Agent{
		ID:      "agent-invalid",
		Name:    "invalid-agent",
		Slug:    "invalid-agent",
		GroveID: "grove-invalid",
		Status:  store.AgentStatusRunning,
	}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}

	reqBody := map[string]interface{}{
		"env": map[string]string{"KEY": "value"},
	}

	path := fmt.Sprintf("/api/v1/groves/grove-invalid/agents/invalid-agent/env")
	rec := doRequest(t, srv, http.MethodPost, path, reqBody)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestEnvGather_HubEnvResolution tests that the Hub resolves env vars from
// its storage (user/grove scopes) during env-gather dispatch.
func TestEnvGather_HubEnvResolution(t *testing.T) {
	ctx := context.Background()
	memStore := createTestStore(t)

	// Create grove
	grove := &store.Grove{ID: "grove-env", Name: "env-grove", Slug: "env-grove"}
	if err := memStore.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	// Create broker
	broker := &store.RuntimeBroker{
		ID: "broker-env", Name: "env-broker", Slug: "env-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := memStore.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	// Store env vars in grove scope
	if err := memStore.CreateEnvVar(ctx, &store.EnvVar{
		ID:      "env-1",
		Key:     "GROVE_API_KEY",
		Value:   "grove-key-value",
		Scope:   "grove",
		ScopeID: "grove-env",
	}); err != nil {
		t.Fatal(err)
	}

	mockClient := &envGatherMockBrokerClient{}
	dispatcher := NewHTTPAgentDispatcherWithClient(memStore, mockClient, true)

	agent := &store.Agent{
		ID:              "agent-env",
		Name:            "env-agent",
		Slug:            "env-agent",
		GroveID:         "grove-env",
		RuntimeBrokerID: "broker-env",
		AppliedConfig: &store.AgentAppliedConfig{
			Harness: "claude",
		},
	}

	_, err := dispatcher.DispatchAgentCreateWithGather(ctx, agent)
	if err != nil {
		t.Fatalf("DispatchAgentCreateWithGather failed: %v", err)
	}

	// The request to the broker should include the grove env var
	if mockClient.lastCreateReq == nil {
		t.Fatal("expected CreateReq to be captured")
	}
	if mockClient.lastCreateReq.ResolvedEnv["GROVE_API_KEY"] != "grove-key-value" {
		t.Errorf("expected GROVE_API_KEY=grove-key-value in resolved env, got %q",
			mockClient.lastCreateReq.ResolvedEnv["GROVE_API_KEY"])
	}
}

// TestEnvGather_HubHandler_RetryAfterCancel_GlobalRoute tests that when an agent
// is stuck in "provisioning" (e.g. env-gather was cancelled) and a new create
// request with GatherEnv=true arrives via the global route, the stale agent is
// deleted and a fresh env-gather flow returns 202.
func TestEnvGather_HubHandler_RetryAfterCancel_GlobalRoute(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	grove := &store.Grove{ID: "grove-retry-global", Name: "retry-global-grove", Slug: "retry-global-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	broker := &store.RuntimeBroker{
		ID: "broker-retry-global", Name: "retry-global-broker", Slug: "retry-global-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := st.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	if err := st.AddGroveProvider(ctx, &store.GroveProvider{
		GroveID: "grove-retry-global", BrokerID: "broker-retry-global",
		LocalPath: "/tmp/test-grove",
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate a previous cancelled env-gather: agent exists in "provisioning" status
	staleAgent := &store.Agent{
		ID:              "stale-agent-global",
		Name:            "retry-agent",
		Slug:            "retry-agent",
		GroveID:         "grove-retry-global",
		RuntimeBrokerID: "broker-retry-global",
		Status:          store.AgentStatusProvisioning,
		AppliedConfig: &store.AgentAppliedConfig{
			Harness: "claude",
		},
	}
	if err := st.CreateAgent(ctx, staleAgent); err != nil {
		t.Fatal(err)
	}

	// Set up dispatcher that returns env requirements (simulating missing env)
	mockClient := &envGatherMockBrokerClient{
		gatherReturnEnvReqs: &RemoteEnvRequirementsResponse{
			AgentID:  "will-be-set",
			Required: []string{"GEMINI_API_KEY"},
			Needs:    []string{"GEMINI_API_KEY"},
		},
	}
	dispatcher := NewHTTPAgentDispatcherWithClient(st, mockClient, true)
	srv.SetDispatcher(dispatcher)

	// Second create request with GatherEnv=true
	reqBody := map[string]interface{}{
		"name":      "retry-agent",
		"groveId":   "grove-retry-global",
		"template":  "claude",
		"gatherEnv": true,
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/agents", reqBody)

	// Should get 202 (env-gather needed), NOT 200 (agent started without env)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CreateAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if resp.EnvGather == nil {
		t.Fatal("expected EnvGather in response (env should be re-gathered)")
	}

	if len(resp.EnvGather.Needs) != 1 || resp.EnvGather.Needs[0] != "GEMINI_API_KEY" {
		t.Errorf("expected needs=[GEMINI_API_KEY], got %v", resp.EnvGather.Needs)
	}

	// The stale agent should have been deleted
	if mockClient.deleteCalled {
		// Dispatcher was used to clean up on broker side - good
	}

	// A new agent should have been created (different ID from stale)
	if resp.Agent == nil {
		t.Fatal("expected agent in response")
	}
	if resp.Agent.ID == "stale-agent-global" {
		t.Error("expected a new agent ID, got the stale agent ID")
	}
	if resp.Agent.Status != store.AgentStatusProvisioning {
		t.Errorf("expected status=%q, got %q", store.AgentStatusProvisioning, resp.Agent.Status)
	}

	// The old agent should no longer exist in the store
	_, err := st.GetAgent(ctx, "stale-agent-global")
	if err != store.ErrNotFound {
		t.Errorf("expected stale agent to be deleted, got err=%v", err)
	}
}

// TestEnvGather_BuildResponse_SecretScope tests that buildEnvGatherResponse
// annotates keys with scope "secret" when the Hub's secret backend has a
// matching secret for the agent's owner or grove.
func TestEnvGather_BuildResponse_SecretScope(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	// Create a user secret for API_KEY
	if err := st.CreateSecret(ctx, &store.Secret{
		ID:             "sec-1",
		Key:            "API_KEY",
		EncryptedValue: "encrypted-val",
		SecretType:     store.SecretTypeEnvironment,
		Target:         "API_KEY",
		Scope:          store.ScopeUser,
		ScopeID:        "owner-1",
	}); err != nil {
		t.Fatal(err)
	}

	// Set up the secret backend on the server
	backend := secret.NewLocalBackend(st)
	srv.SetSecretBackend(backend)

	agent := &store.Agent{
		ID:      "agent-scope-test",
		Name:    "scope-test-agent",
		OwnerID: "owner-1",
		GroveID: "grove-1",
	}

	brokerReqs := &RemoteEnvRequirementsResponse{
		AgentID:  "agent-scope-test",
		Required: []string{"API_KEY", "OTHER_KEY"},
		HubHas:   []string{"API_KEY", "OTHER_KEY"},
		Needs:    []string{},
	}

	resp := srv.buildEnvGatherResponse(ctx, agent, brokerReqs)

	// Verify API_KEY is annotated as "secret"
	var apiKeySource, otherKeySource *EnvSource
	for i := range resp.HubHas {
		switch resp.HubHas[i].Key {
		case "API_KEY":
			apiKeySource = &resp.HubHas[i]
		case "OTHER_KEY":
			otherKeySource = &resp.HubHas[i]
		}
	}

	if apiKeySource == nil {
		t.Fatal("expected API_KEY in hubHas")
	}
	if apiKeySource.Scope != "secret" {
		t.Errorf("expected API_KEY scope='secret', got %q", apiKeySource.Scope)
	}

	if otherKeySource == nil {
		t.Fatal("expected OTHER_KEY in hubHas")
	}
	// OTHER_KEY has no matching secret, so it stays as "hub"
	if otherKeySource.Scope != "hub" {
		t.Errorf("expected OTHER_KEY scope='hub', got %q", otherKeySource.Scope)
	}
}

// TestEnvGather_SecretInfoRelay tests that SecretInfo is relayed from the
// broker response through to the CLI-facing EnvGatherResponse.
func TestEnvGather_SecretInfoRelay(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	grove := &store.Grove{ID: "grove-si-relay", Name: "si-relay-grove", Slug: "si-relay-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	broker := &store.RuntimeBroker{
		ID: "broker-si-relay", Name: "si-relay-broker", Slug: "si-relay-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := st.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	if err := st.AddGroveProvider(ctx, &store.GroveProvider{
		GroveID: "grove-si-relay", BrokerID: "broker-si-relay",
		LocalPath: "/tmp/test-grove",
	}); err != nil {
		t.Fatal(err)
	}

	// Mock broker returns SecretInfo in env requirements
	mockClient := &envGatherMockBrokerClient{
		gatherReturnEnvReqs: &RemoteEnvRequirementsResponse{
			AgentID:  "will-be-set",
			Required: []string{"API_KEY", "CUSTOM_TOKEN"},
			Needs:    []string{"CUSTOM_TOKEN"},
			HubHas:   []string{"API_KEY"},
			SecretInfo: map[string]SecretKeyInfo{
				"CUSTOM_TOKEN": {Description: "Token for custom integration", Source: "settings"},
			},
		},
	}
	dispatcher := NewHTTPAgentDispatcherWithClient(st, mockClient, true)
	srv.SetDispatcher(dispatcher)

	reqBody := map[string]interface{}{
		"name":      "si-relay-agent",
		"groveId":   "grove-si-relay",
		"template":  "claude",
		"gatherEnv": true,
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/agents", reqBody)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CreateAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if resp.EnvGather == nil {
		t.Fatal("expected EnvGather to be set")
	}

	// SecretInfo should be relayed
	if resp.EnvGather.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be relayed")
	}
	info, ok := resp.EnvGather.SecretInfo["CUSTOM_TOKEN"]
	if !ok {
		t.Fatal("expected CUSTOM_TOKEN in SecretInfo")
	}
	if info.Description != "Token for custom integration" {
		t.Errorf("expected description='Token for custom integration', got %q", info.Description)
	}
	if info.Source != "settings" {
		t.Errorf("expected source='settings', got %q", info.Source)
	}
}

// TestEnvGather_SecretInfoRelayType tests that the Type field is relayed from
// the broker response through the Hub to the CLI-facing EnvGatherResponse.
func TestEnvGather_SecretInfoRelayType(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	grove := &store.Grove{ID: "grove-si-type", Name: "si-type-grove", Slug: "si-type-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	broker := &store.RuntimeBroker{
		ID: "broker-si-type", Name: "si-type-broker", Slug: "si-type-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := st.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	if err := st.AddGroveProvider(ctx, &store.GroveProvider{
		GroveID: "grove-si-type", BrokerID: "broker-si-type",
		LocalPath: "/tmp/test-grove",
	}); err != nil {
		t.Fatal(err)
	}

	// Mock broker returns SecretInfo with Type fields
	mockClient := &envGatherMockBrokerClient{
		gatherReturnEnvReqs: &RemoteEnvRequirementsResponse{
			AgentID:  "will-be-set",
			Required: []string{"ENV_SECRET", "FILE_CERT", "VAR_TOKEN"},
			Needs:    []string{"ENV_SECRET", "FILE_CERT", "VAR_TOKEN"},
			SecretInfo: map[string]SecretKeyInfo{
				"ENV_SECRET": {Description: "Environment secret", Source: "settings", Type: "environment"},
				"FILE_CERT":  {Description: "TLS certificate", Source: "template", Type: "file"},
				"VAR_TOKEN":  {Description: "Variable token", Source: "settings", Type: "variable"},
			},
		},
	}
	dispatcher := NewHTTPAgentDispatcherWithClient(st, mockClient, true)
	srv.SetDispatcher(dispatcher)

	reqBody := map[string]interface{}{
		"name":      "si-type-agent",
		"groveId":   "grove-si-type",
		"template":  "claude",
		"gatherEnv": true,
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/agents", reqBody)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CreateAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if resp.EnvGather == nil {
		t.Fatal("expected EnvGather to be set")
	}
	if resp.EnvGather.SecretInfo == nil {
		t.Fatal("expected SecretInfo to be relayed")
	}

	// Verify Type is relayed for each key
	tests := []struct {
		key      string
		wantType string
	}{
		{"ENV_SECRET", "environment"},
		{"FILE_CERT", "file"},
		{"VAR_TOKEN", "variable"},
	}
	for _, tc := range tests {
		info, ok := resp.EnvGather.SecretInfo[tc.key]
		if !ok {
			t.Errorf("expected %s in SecretInfo", tc.key)
			continue
		}
		if info.Type != tc.wantType {
			t.Errorf("expected %s type=%q, got %q", tc.key, tc.wantType, info.Type)
		}
	}
}

// TestEnvGather_HubHandler_RetryAfterCancel_GroveRoute tests the same retry
// scenario via the grove-scoped route /api/v1/groves/{groveId}/agents.
func TestEnvGather_HubHandler_RetryAfterCancel_GroveRoute(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	grove := &store.Grove{ID: "grove-retry-route", Name: "retry-route-grove", Slug: "retry-route-grove"}
	if err := st.CreateGrove(ctx, grove); err != nil {
		t.Fatal(err)
	}

	broker := &store.RuntimeBroker{
		ID: "broker-retry-route", Name: "retry-route-broker", Slug: "retry-route-broker",
		Endpoint: "http://localhost:9800", Status: store.BrokerStatusOnline,
	}
	if err := st.CreateRuntimeBroker(ctx, broker); err != nil {
		t.Fatal(err)
	}

	if err := st.AddGroveProvider(ctx, &store.GroveProvider{
		GroveID: "grove-retry-route", BrokerID: "broker-retry-route",
		LocalPath: "/tmp/test-grove",
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate a previous cancelled env-gather: agent exists in "provisioning" status
	staleAgent := &store.Agent{
		ID:              "stale-agent-route",
		Name:            "retry-route-agent",
		Slug:            "retry-route-agent",
		GroveID:         "grove-retry-route",
		RuntimeBrokerID: "broker-retry-route",
		Status:          store.AgentStatusProvisioning,
		AppliedConfig: &store.AgentAppliedConfig{
			Harness: "claude",
		},
	}
	if err := st.CreateAgent(ctx, staleAgent); err != nil {
		t.Fatal(err)
	}

	// Set up dispatcher that returns env requirements (simulating missing env)
	mockClient := &envGatherMockBrokerClient{
		gatherReturnEnvReqs: &RemoteEnvRequirementsResponse{
			AgentID:  "will-be-set",
			Required: []string{"GEMINI_API_KEY"},
			Needs:    []string{"GEMINI_API_KEY"},
		},
	}
	dispatcher := NewHTTPAgentDispatcherWithClient(st, mockClient, true)
	srv.SetDispatcher(dispatcher)

	// Second create request via grove-scoped route with GatherEnv=true
	reqBody := map[string]interface{}{
		"name":      "retry-route-agent",
		"template":  "claude",
		"gatherEnv": true,
	}

	rec := doRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/groves/%s/agents", grove.ID), reqBody)

	// Should get 202 (env-gather needed), NOT 200 (agent started without env)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CreateAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal("failed to decode response:", err)
	}

	if resp.EnvGather == nil {
		t.Fatal("expected EnvGather in response (env should be re-gathered)")
	}

	if len(resp.EnvGather.Needs) != 1 || resp.EnvGather.Needs[0] != "GEMINI_API_KEY" {
		t.Errorf("expected needs=[GEMINI_API_KEY], got %v", resp.EnvGather.Needs)
	}

	// A new agent should have been created (different ID from stale)
	if resp.Agent == nil {
		t.Fatal("expected agent in response")
	}
	if resp.Agent.ID == "stale-agent-route" {
		t.Error("expected a new agent ID, got the stale agent ID")
	}
	if resp.Agent.Status != store.AgentStatusProvisioning {
		t.Errorf("expected status=%q, got %q", store.AgentStatusProvisioning, resp.Agent.Status)
	}

	// The old agent should no longer exist in the store
	_, err := st.GetAgent(ctx, "stale-agent-route")
	if err != store.ErrNotFound {
		t.Errorf("expected stale agent to be deleted, got err=%v", err)
	}
}
