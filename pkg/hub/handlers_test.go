package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ptone/scion-agent/pkg/store"
	"github.com/ptone/scion-agent/pkg/store/sqlite"
)

// testServer creates a test server with an in-memory SQLite store.
func testServer(t *testing.T) (*Server, store.Store) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate test store: %v", err)
	}

	cfg := DefaultServerConfig()
	srv := New(cfg, s)
	return srv, s
}

// doRequest performs an HTTP request against the test server.
func doRequest(t *testing.T, srv *Server, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal body: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// ============================================================================
// Health Endpoint Tests
// ============================================================================

func TestHealthz(t *testing.T) {
	srv, _ := testServer(t)

	rec := doRequest(t, srv, http.MethodGet, "/healthz", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", resp.Status)
	}
}

func TestReadyz(t *testing.T) {
	srv, _ := testServer(t)

	rec := doRequest(t, srv, http.MethodGet, "/readyz", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ready" {
		t.Errorf("expected status 'ready', got %q", resp["status"])
	}
}

// ============================================================================
// Agent Endpoint Tests
// ============================================================================

func TestAgentList(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	// Create a grove first (agents reference groves)
	grove := &store.Grove{
		ID:        "grove_test123",
		Slug:      "test-grove",
		Name:      "Test Grove",
		GitRemote: "https://github.com/test/repo",
		Created:   time.Now(),
		Updated:   time.Now(),
	}
	if err := s.CreateGrove(ctx, grove); err != nil {
		t.Fatalf("failed to create grove: %v", err)
	}

	// Create some test agents
	for i := 0; i < 3; i++ {
		agent := &store.Agent{
			ID:           "agent_" + string(rune('a'+i)),
			AgentID:      "test-agent-" + string(rune('a'+i)),
			Name:         "Test Agent " + string(rune('A'+i)),
			GroveID:      grove.ID,
			Status:       store.AgentStatusStopped,
			StateVersion: 1,
			Created:      time.Now(),
			Updated:      time.Now(),
		}
		if err := s.CreateAgent(ctx, agent); err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/agents", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ListAgentsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(resp.Agents))
	}

	if resp.TotalCount != 3 {
		t.Errorf("expected total 3, got %d", resp.TotalCount)
	}
}

func TestAgentCreate(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	// Create a runtime host first
	host := &store.RuntimeHost{
		ID:     "host_test123",
		Slug:   "test-host",
		Name:   "Test Host",
		Type:   "docker",
		Mode:   store.HostModeConnected,
		Status: store.HostStatusOnline,
	}
	if err := s.CreateRuntimeHost(ctx, host); err != nil {
		t.Fatalf("failed to create runtime host: %v", err)
	}

	// Create a grove with default runtime host
	grove := &store.Grove{
		ID:                   "grove_abc123",
		Slug:                 "my-grove",
		Name:                 "My Grove",
		GitRemote:            "github.com/test/repo",
		DefaultRuntimeHostID: host.ID,
		Created:              time.Now(),
		Updated:              time.Now(),
	}
	if err := s.CreateGrove(ctx, grove); err != nil {
		t.Fatalf("failed to create grove: %v", err)
	}

	// Register the host as a contributor to the grove
	contrib := &store.GroveContributor{
		GroveID:  grove.ID,
		HostID:   host.ID,
		HostName: host.Name,
		Mode:     host.Mode,
		Status:   store.HostStatusOnline,
	}
	if err := s.AddGroveContributor(ctx, contrib); err != nil {
		t.Fatalf("failed to add grove contributor: %v", err)
	}

	body := map[string]interface{}{
		"name":    "New Agent",
		"groveId": grove.ID,
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/agents", body)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CreateAgentResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Agent == nil {
		t.Fatal("expected agent to be set")
	}

	if resp.Agent.AgentID != "new-agent" {
		t.Errorf("expected agentId 'new-agent', got %q", resp.Agent.AgentID)
	}

	if resp.Agent.ID == "" {
		t.Error("expected ID to be set")
	}

	if resp.Agent.Status != store.AgentStatusPending {
		t.Errorf("expected status 'pending', got %q", resp.Agent.Status)
	}

	if resp.Agent.RuntimeHostID != host.ID {
		t.Errorf("expected runtimeHostId %q, got %q", host.ID, resp.Agent.RuntimeHostID)
	}
}

func TestAgentGetByID(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	// Create grove and agent
	grove := &store.Grove{
		ID:        "grove_xyz",
		Slug:      "grove-xyz",
		Name:      "Grove XYZ",
		GitRemote: "https://github.com/test/repo",
		Created:   time.Now(),
		Updated:   time.Now(),
	}
	if err := s.CreateGrove(ctx, grove); err != nil {
		t.Fatalf("failed to create grove: %v", err)
	}

	agent := &store.Agent{
		ID:           "agent_test1",
		AgentID:      "test-agent",
		Name:         "Test Agent",
		GroveID:      grove.ID,
		Status:       store.AgentStatusStopped,
		StateVersion: 1,
		Created:      time.Now(),
		Updated:      time.Now(),
	}
	if err := s.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/agents/agent_test1", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.Agent
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ID != "agent_test1" {
		t.Errorf("expected ID 'agent_test1', got %q", resp.ID)
	}
}

func TestAgentNotFound(t *testing.T) {
	srv, _ := testServer(t)

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/agents/nonexistent", nil)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error.Code != ErrCodeNotFound {
		t.Errorf("expected error code %q, got %q", ErrCodeNotFound, resp.Error.Code)
	}
}

func TestAgentDelete(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	// Create grove and agent
	grove := &store.Grove{
		ID:        "grove_del",
		Slug:      "grove-del",
		Name:      "Grove Del",
		GitRemote: "https://github.com/test/repo",
		Created:   time.Now(),
		Updated:   time.Now(),
	}
	if err := s.CreateGrove(ctx, grove); err != nil {
		t.Fatalf("failed to create grove: %v", err)
	}

	agent := &store.Agent{
		ID:           "agent_delete",
		AgentID:      "delete-me",
		Name:         "Delete Me",
		GroveID:      grove.ID,
		Status:       store.AgentStatusStopped,
		StateVersion: 1,
		Created:      time.Now(),
		Updated:      time.Now(),
	}
	if err := s.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	rec := doRequest(t, srv, http.MethodDelete, "/api/v1/agents/agent_delete", nil)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify agent is deleted
	_, err := s.GetAgent(ctx, "agent_delete")
	if err == nil {
		t.Error("expected agent to be deleted")
	}
}

// ============================================================================
// Grove Endpoint Tests
// ============================================================================

func TestGroveList(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		grove := &store.Grove{
			ID:        "grove_" + string(rune('a'+i)),
			Slug:      "grove-" + string(rune('a'+i)),
			Name:      "Grove " + string(rune('A'+i)),
			GitRemote: "https://github.com/test/repo" + string(rune('a'+i)),
			Created:   time.Now(),
			Updated:   time.Now(),
		}
		if err := s.CreateGrove(ctx, grove); err != nil {
			t.Fatalf("failed to create grove: %v", err)
		}
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/groves", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ListGrovesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Groves) != 2 {
		t.Errorf("expected 2 groves, got %d", len(resp.Groves))
	}
}

func TestGroveRegister(t *testing.T) {
	srv, _ := testServer(t)

	body := map[string]interface{}{
		"gitRemote": "https://github.com/test/my-project.git",
		"name":      "My Project",
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/groves/register", body)

	// Grove register always returns 200 (idempotent)
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp RegisterGroveResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Grove.ID == "" {
		t.Error("expected grove ID to be set")
	}

	if !resp.Created {
		t.Error("expected created to be true for new grove")
	}

	// The git remote should be normalized (no scheme, no .git suffix)
	if resp.Grove.GitRemote != "github.com/test/my-project" {
		t.Errorf("expected normalized git remote 'github.com/test/my-project', got %q", resp.Grove.GitRemote)
	}
}

func TestGroveRegisterIdempotent(t *testing.T) {
	srv, _ := testServer(t)

	body := map[string]interface{}{
		"gitRemote": "https://github.com/test/idempotent-repo",
		"name":      "Idempotent Repo",
	}

	// First registration
	rec1 := doRequest(t, srv, http.MethodPost, "/api/v1/groves/register", body)
	if rec1.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec1.Code, rec1.Body.String())
	}

	var resp1 RegisterGroveResponse
	if err := json.NewDecoder(rec1.Body).Decode(&resp1); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp1.Created {
		t.Error("expected created to be true for first registration")
	}

	// Second registration with same git remote
	rec2 := doRequest(t, srv, http.MethodPost, "/api/v1/groves/register", body)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected status 200 for idempotent call, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var resp2 RegisterGroveResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should return the same grove
	if resp1.Grove.ID != resp2.Grove.ID {
		t.Errorf("expected same grove ID on idempotent call, got %q and %q", resp1.Grove.ID, resp2.Grove.ID)
	}

	// Second call should not have created=true
	if resp2.Created {
		t.Error("expected created to be false on second call")
	}
}

func TestGroveGetByID(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	grove := &store.Grove{
		ID:        "grove_gettest",
		Slug:      "get-test",
		Name:      "Get Test",
		GitRemote: "https://github.com/test/get-test",
		Created:   time.Now(),
		Updated:   time.Now(),
	}
	if err := s.CreateGrove(ctx, grove); err != nil {
		t.Fatalf("failed to create grove: %v", err)
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/groves/grove_gettest", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.Grove
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ID != "grove_gettest" {
		t.Errorf("expected ID 'grove_gettest', got %q", resp.ID)
	}
}

// ============================================================================
// RuntimeHost Endpoint Tests
// ============================================================================

func TestRuntimeHostList(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	host := &store.RuntimeHost{
		ID:            "host_test1",
		Name:          "Test Host",
		Slug:          "test-host",
		Type:          "docker",
		Mode:          store.HostModeConnected,
		Status:        store.HostStatusOnline,
		LastHeartbeat: time.Now(),
		Created:       time.Now(),
		Updated:       time.Now(),
	}
	if err := s.CreateRuntimeHost(ctx, host); err != nil {
		t.Fatalf("failed to create runtime host: %v", err)
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/runtime-hosts", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ListRuntimeHostsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Hosts) != 1 {
		t.Errorf("expected 1 host, got %d", len(resp.Hosts))
	}
}

func TestRuntimeHostGetByID(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	host := &store.RuntimeHost{
		ID:            "host_gettest",
		Name:          "Get Test Host",
		Slug:          "get-test-host",
		Type:          "docker",
		Mode:          store.HostModeConnected,
		Status:        store.HostStatusOnline,
		LastHeartbeat: time.Now(),
		Created:       time.Now(),
		Updated:       time.Now(),
	}
	if err := s.CreateRuntimeHost(ctx, host); err != nil {
		t.Fatalf("failed to create runtime host: %v", err)
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/runtime-hosts/host_gettest", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.RuntimeHost
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ID != "host_gettest" {
		t.Errorf("expected ID 'host_gettest', got %q", resp.ID)
	}
}

// ============================================================================
// Template Endpoint Tests
// ============================================================================

func TestTemplateList(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	template := &store.Template{
		ID:         "tmpl_test1",
		Slug:       "test-template",
		Name:       "Test Template",
		Harness:    "claude",
		Scope:      "global",
		Visibility: store.VisibilityPublic,
		Created:    time.Now(),
		Updated:    time.Now(),
	}
	if err := s.CreateTemplate(ctx, template); err != nil {
		t.Fatalf("failed to create template: %v", err)
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/templates", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ListTemplatesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(resp.Templates))
	}
}

func TestTemplateCreate(t *testing.T) {
	srv, _ := testServer(t)

	body := map[string]interface{}{
		"slug":       "new-template",
		"name":       "New Template",
		"harness":    "claude",
		"scope":      "global",
		"visibility": "private",
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/templates", body)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.Template
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Slug != "new-template" {
		t.Errorf("expected slug 'new-template', got %q", resp.Slug)
	}

	if resp.Visibility != store.VisibilityPrivate {
		t.Errorf("expected visibility 'private', got %q", resp.Visibility)
	}
}

// ============================================================================
// User Endpoint Tests
// ============================================================================

func TestUserList(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	user := &store.User{
		ID:          "user_test1",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        store.UserRoleMember,
		Status:      "active",
		Created:     time.Now(),
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	rec := doRequest(t, srv, http.MethodGet, "/api/v1/users", nil)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ListUsersResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Users) != 1 {
		t.Errorf("expected 1 user, got %d", len(resp.Users))
	}
}

func TestUserCreate(t *testing.T) {
	srv, _ := testServer(t)

	body := map[string]interface{}{
		"email":       "newuser@example.com",
		"displayName": "New User",
		"role":        "admin",
	}

	rec := doRequest(t, srv, http.MethodPost, "/api/v1/users", body)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.User
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Email != "newuser@example.com" {
		t.Errorf("expected email 'newuser@example.com', got %q", resp.Email)
	}

	if resp.Role != store.UserRoleAdmin {
		t.Errorf("expected role 'admin', got %q", resp.Role)
	}
}

// ============================================================================
// Error Handling Tests
// ============================================================================

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := testServer(t)

	// Try PATCH on /healthz which doesn't support it
	rec := doRequest(t, srv, http.MethodPatch, "/healthz", nil)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidJSON(t *testing.T) {
	srv, s := testServer(t)
	ctx := context.Background()

	// Create a grove first
	grove := &store.Grove{
		ID:        "grove_invalid",
		Slug:      "invalid-grove",
		Name:      "Invalid Grove",
		GitRemote: "https://github.com/test/invalid",
		Created:   time.Now(),
		Updated:   time.Now(),
	}
	if err := s.CreateGrove(ctx, grove); err != nil {
		t.Fatalf("failed to create grove: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader([]byte("{invalid json")))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ============================================================================
// CORS Tests
// ============================================================================

func TestCORSHeaders(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:3000")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	corsOrigin := rec.Header().Get("Access-Control-Allow-Origin")
	if corsOrigin != "http://localhost:3000" {
		t.Errorf("expected CORS origin 'http://localhost:3000', got %q", corsOrigin)
	}
}

func TestCORSPreflight(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/agents", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rec.Code)
	}

	corsOrigin := rec.Header().Get("Access-Control-Allow-Origin")
	if corsOrigin != "http://localhost:3000" {
		t.Errorf("expected CORS origin 'http://localhost:3000', got %q", corsOrigin)
	}
}
