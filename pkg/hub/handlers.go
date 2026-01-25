package hub

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ptone/scion-agent/pkg/api"
	"github.com/ptone/scion-agent/pkg/store"
)

// ============================================================================
// Health Endpoints
// ============================================================================

type HealthResponse struct {
	Status  string            `json:"status"`
	Version string            `json:"version"`
	Uptime  string            `json:"uptime"`
	Checks  map[string]string `json:"checks,omitempty"`
	Stats   *HealthStats      `json:"stats,omitempty"`
}

type HealthStats struct {
	ConnectedHosts int `json:"connectedHosts,omitempty"`
	ActiveAgents   int `json:"activeAgents,omitempty"`
	Groves         int `json:"groves,omitempty"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		MethodNotAllowed(w)
		return
	}

	checks := make(map[string]string)

	// Check database
	if err := s.store.Ping(r.Context()); err != nil {
		checks["database"] = "unhealthy"
	} else {
		checks["database"] = "healthy"
	}

	// Get stats
	stats := &HealthStats{}
	if agentResult, err := s.store.ListAgents(r.Context(), store.AgentFilter{Status: store.AgentStatusRunning}, store.ListOptions{Limit: 1}); err == nil {
		stats.ActiveAgents = agentResult.TotalCount
	}
	if groveResult, err := s.store.ListGroves(r.Context(), store.GroveFilter{}, store.ListOptions{Limit: 1}); err == nil {
		stats.Groves = groveResult.TotalCount
	}
	if hostResult, err := s.store.ListRuntimeHosts(r.Context(), store.RuntimeHostFilter{Status: store.HostStatusOnline}, store.ListOptions{Limit: 1}); err == nil {
		stats.ConnectedHosts = hostResult.TotalCount
	}

	status := "healthy"
	for _, v := range checks {
		if v != "healthy" {
			status = "degraded"
			break
		}
	}

	resp := HealthResponse{
		Status:  status,
		Version: "0.1.0", // TODO: Get from build info
		Uptime:  time.Since(s.startTime).Round(time.Second).String(),
		Checks:  checks,
		Stats:   stats,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		MethodNotAllowed(w)
		return
	}

	// Check if database is connected and migrated
	if err := s.store.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"reason": "database not available",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ready",
	})
}

// ============================================================================
// Agent Endpoints
// ============================================================================

type ListAgentsResponse struct {
	Agents     []store.Agent `json:"agents"`
	NextCursor string        `json:"nextCursor,omitempty"`
	TotalCount int           `json:"totalCount"`
}

type CreateAgentRequest struct {
	Name      string            `json:"name"`
	GroveID   string            `json:"groveId"`
	Template  string            `json:"template"`
	Task      string            `json:"task,omitempty"`
	Branch    string            `json:"branch,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Config    *AgentConfigOverride `json:"config,omitempty"`
}

type AgentConfigOverride struct {
	Image    string            `json:"image,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Detached *bool             `json:"detached,omitempty"`
	Model    string            `json:"model,omitempty"`
}

type CreateAgentResponse struct {
	Agent    *store.Agent `json:"agent"`
	Warnings []string     `json:"warnings,omitempty"`
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listAgents(w, r)
	case http.MethodPost:
		s.createAgent(w, r)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	filter := store.AgentFilter{
		GroveID:       query.Get("groveId"),
		RuntimeHostID: query.Get("runtimeHostId"),
		Status:        query.Get("status"),
	}

	limit := 50
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	result, err := s.store.ListAgents(ctx, filter, store.ListOptions{
		Limit:  limit,
		Cursor: query.Get("cursor"),
	})
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, ListAgentsResponse{
		Agents:     result.Items,
		NextCursor: result.NextCursor,
		TotalCount: result.TotalCount,
	})
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Name == "" {
		ValidationError(w, "name is required", nil)
		return
	}
	if req.GroveID == "" {
		ValidationError(w, "groveId is required", nil)
		return
	}

	// Verify grove exists
	if _, err := s.store.GetGrove(ctx, req.GroveID); err != nil {
		if err == store.ErrNotFound {
			NotFound(w, "Grove")
			return
		}
		writeErrorFromErr(w, err, "")
		return
	}

	// Create agent
	agent := &store.Agent{
		ID:         api.NewUUID(),
		AgentID:    api.Slugify(req.Name),
		Name:       req.Name,
		Template:   req.Template,
		GroveID:    req.GroveID,
		Status:     store.AgentStatusPending,
		Labels:     req.Labels,
		Visibility: store.VisibilityPrivate,
	}

	if req.Config != nil {
		agent.Image = req.Config.Image
		if req.Config.Detached != nil {
			agent.Detached = *req.Config.Detached
		} else {
			agent.Detached = true
		}
		agent.AppliedConfig = &store.AgentAppliedConfig{
			Image:   req.Config.Image,
			Env:     req.Config.Env,
			Model:   req.Config.Model,
			Harness: req.Template,
		}
	} else {
		agent.Detached = true
	}

	if err := s.store.CreateAgent(ctx, agent); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	// If a dispatcher is available (co-located runtime host), dispatch the agent
	// This enables zero-friction handoff - the agent will appear in both Hub and Runtime Host
	var warnings []string
	if dispatcher := s.GetDispatcher(); dispatcher != nil {
		if err := dispatcher.DispatchAgentCreate(ctx, agent); err != nil {
			// Log the error but don't fail the request - agent is created in Hub
			warnings = append(warnings, "Failed to dispatch to runtime host: "+err.Error())
			// The agent remains in pending status
		} else {
			// Update agent status to reflect it's being started
			agent.Status = store.AgentStatusProvisioning
			if err := s.store.UpdateAgent(ctx, agent); err != nil {
				warnings = append(warnings, "Failed to update agent status: "+err.Error())
			}
		}
	}

	writeJSON(w, http.StatusCreated, CreateAgentResponse{
		Agent:    agent,
		Warnings: warnings,
	})
}

func (s *Server) handleAgentByID(w http.ResponseWriter, r *http.Request) {
	id, action := extractAction(r, "/api/v1/agents")

	if id == "" {
		NotFound(w, "Agent")
		return
	}

	// Handle actions
	if action != "" {
		s.handleAgentAction(w, r, id, action)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getAgent(w, r, id)
	case http.MethodPatch:
		s.updateAgent(w, r, id)
	case http.MethodDelete:
		s.deleteAgent(w, r, id)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request, id string) {
	agent, err := s.store.GetAgent(r.Context(), id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	agent, err := s.store.GetAgent(ctx, id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	var updates struct {
		Name         string            `json:"name,omitempty"`
		Labels       map[string]string `json:"labels,omitempty"`
		Annotations  map[string]string `json:"annotations,omitempty"`
		TaskSummary  string            `json:"taskSummary,omitempty"`
		StateVersion int64             `json:"stateVersion"`
	}

	if err := readJSON(r, &updates); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Check version for optimistic locking
	if updates.StateVersion != 0 && updates.StateVersion != agent.StateVersion {
		Conflict(w, "Version conflict - resource was modified")
		return
	}

	// Apply updates
	if updates.Name != "" {
		agent.Name = updates.Name
	}
	if updates.Labels != nil {
		agent.Labels = updates.Labels
	}
	if updates.Annotations != nil {
		agent.Annotations = updates.Annotations
	}
	if updates.TaskSummary != "" {
		agent.TaskSummary = updates.TaskSummary
	}

	if err := s.store.UpdateAgent(ctx, agent); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.store.DeleteAgent(r.Context(), id); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		MethodNotAllowed(w)
		return
	}

	switch action {
	case "status":
		s.updateAgentStatus(w, r, id)
	case "start", "stop", "restart":
		// These would typically be forwarded to the runtime host
		// For now, just update status
		s.handleAgentLifecycle(w, r, id, action)
	default:
		NotFound(w, "Action")
	}
}

func (s *Server) updateAgentStatus(w http.ResponseWriter, r *http.Request, id string) {
	var status store.AgentStatusUpdate
	if err := readJSON(r, &status); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if err := s.store.UpdateAgentStatus(r.Context(), id, status); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAgentLifecycle(w http.ResponseWriter, r *http.Request, id, action string) {
	ctx := r.Context()

	agent, err := s.store.GetAgent(ctx, id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	var newStatus string
	switch action {
	case "start":
		newStatus = store.AgentStatusRunning
	case "stop":
		newStatus = store.AgentStatusStopped
	case "restart":
		newStatus = store.AgentStatusRunning
	}

	if err := s.store.UpdateAgentStatus(ctx, id, store.AgentStatusUpdate{
		Status: newStatus,
	}); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	agent.Status = newStatus
	writeJSON(w, http.StatusOK, agent)
}

// ============================================================================
// Grove Endpoints
// ============================================================================

type ListGrovesResponse struct {
	Groves     []store.Grove `json:"groves"`
	NextCursor string        `json:"nextCursor,omitempty"`
	TotalCount int           `json:"totalCount"`
}

type CreateGroveRequest struct {
	Name       string            `json:"name"`
	GitRemote  string            `json:"gitRemote,omitempty"`
	Visibility string            `json:"visibility,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type RegisterGroveRequest struct {
	Name      string            `json:"name"`
	GitRemote string            `json:"gitRemote"`
	Path      string            `json:"path,omitempty"`
	Host      *RegisterHostInfo `json:"host,omitempty"`
	Profiles  []string          `json:"profiles,omitempty"`
	Mode      string            `json:"mode,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type RegisterHostInfo struct {
	ID                 string               `json:"id,omitempty"`
	Name               string               `json:"name"`
	Version            string               `json:"version,omitempty"`
	Capabilities       *store.HostCapabilities `json:"capabilities,omitempty"`
	Runtimes           []store.HostRuntime  `json:"runtimes,omitempty"`
	SupportedHarnesses []string             `json:"supportedHarnesses,omitempty"`
}

type RegisterGroveResponse struct {
	Grove     *store.Grove       `json:"grove"`
	Host      *store.RuntimeHost `json:"host,omitempty"`
	Created   bool               `json:"created"`
	HostToken string             `json:"hostToken,omitempty"`
}

func (s *Server) handleGroves(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listGroves(w, r)
	case http.MethodPost:
		s.createGrove(w, r)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) listGroves(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	filter := store.GroveFilter{
		Visibility:      query.Get("visibility"),
		GitRemotePrefix: query.Get("gitRemote"),
		HostID:          query.Get("hostId"),
	}

	limit := 50
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	result, err := s.store.ListGroves(ctx, filter, store.ListOptions{
		Limit:  limit,
		Cursor: query.Get("cursor"),
	})
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, ListGrovesResponse{
		Groves:     result.Items,
		NextCursor: result.NextCursor,
		TotalCount: result.TotalCount,
	})
}

func (s *Server) createGrove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req CreateGroveRequest
	if err := readJSON(r, &req); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if req.Name == "" {
		ValidationError(w, "name is required", nil)
		return
	}

	grove := &store.Grove{
		ID:         api.NewUUID(),
		Name:       req.Name,
		Slug:       api.Slugify(req.Name),
		GitRemote:  normalizeGitRemote(req.GitRemote),
		Labels:     req.Labels,
		Visibility: req.Visibility,
	}

	if grove.Visibility == "" {
		grove.Visibility = store.VisibilityPrivate
	}

	if err := s.store.CreateGrove(ctx, grove); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusCreated, grove)
}

func (s *Server) handleGroveRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		MethodNotAllowed(w)
		return
	}

	ctx := r.Context()

	var req RegisterGroveRequest
	if err := readJSON(r, &req); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if req.Name == "" {
		ValidationError(w, "name is required", nil)
		return
	}

	normalizedRemote := normalizeGitRemote(req.GitRemote)

	// Try to find existing grove by git remote
	var grove *store.Grove
	var created bool

	if normalizedRemote != "" {
		existingGrove, err := s.store.GetGroveByGitRemote(ctx, normalizedRemote)
		if err == nil {
			grove = existingGrove
		} else if err != store.ErrNotFound {
			writeErrorFromErr(w, err, "")
			return
		}
	}

	// Create new grove if not found
	if grove == nil {
		grove = &store.Grove{
			ID:         api.NewUUID(),
			Name:       req.Name,
			Slug:       api.Slugify(req.Name),
			GitRemote:  normalizedRemote,
			Labels:     req.Labels,
			Visibility: store.VisibilityPrivate,
		}

		if err := s.store.CreateGrove(ctx, grove); err != nil {
			writeErrorFromErr(w, err, "")
			return
		}
		created = true
	}

	// Handle host registration if provided
	var host *store.RuntimeHost
	var hostToken string

	if req.Host != nil {
		hostID := req.Host.ID
		if hostID == "" {
			hostID = api.NewUUID()
		}

		// Create or update host
		host = &store.RuntimeHost{
			ID:                 hostID,
			Name:               req.Host.Name,
			Slug:               api.Slugify(req.Host.Name),
			Type:               "docker", // Default
			Mode:               req.Mode,
			Version:            req.Host.Version,
			Status:             store.HostStatusOnline,
			ConnectionState:    "connected",
			Capabilities:       req.Host.Capabilities,
			SupportedHarnesses: req.Host.SupportedHarnesses,
			Runtimes:           req.Host.Runtimes,
		}

		if host.Mode == "" {
			host.Mode = store.HostModeConnected
		}

		// Determine runtime type from runtimes list
		if len(req.Host.Runtimes) > 0 {
			host.Type = req.Host.Runtimes[0].Type
		}

		// Try to get existing host
		existingHost, err := s.store.GetRuntimeHost(ctx, hostID)
		if err == nil {
			// Update existing host
			host.Created = existingHost.Created
			if err := s.store.UpdateRuntimeHost(ctx, host); err != nil {
				writeErrorFromErr(w, err, "")
				return
			}
		} else if err == store.ErrNotFound {
			// Create new host
			if err := s.store.CreateRuntimeHost(ctx, host); err != nil {
				writeErrorFromErr(w, err, "")
				return
			}
		} else {
			writeErrorFromErr(w, err, "")
			return
		}

		// Add as grove contributor
		contrib := &store.GroveContributor{
			GroveID:  grove.ID,
			HostID:   host.ID,
			HostName: host.Name,
			Mode:     host.Mode,
			Status:   store.HostStatusOnline,
			Profiles: req.Profiles,
		}

		if err := s.store.AddGroveContributor(ctx, contrib); err != nil {
			writeErrorFromErr(w, err, "")
			return
		}

		// Generate a simple token (in production, use proper token generation)
		hostToken = "host_" + api.NewShortID() + "_" + api.NewShortID()
	}

	writeJSON(w, http.StatusOK, RegisterGroveResponse{
		Grove:     grove,
		Host:      host,
		Created:   created,
		HostToken: hostToken,
	})
}

// handleGroveRoutes routes requests under /api/v1/groves/{groveId}/...
// It supports both the grove resource endpoints and nested agent endpoints.
func (s *Server) handleGroveRoutes(w http.ResponseWriter, r *http.Request) {
	// Extract grove ID and remaining path
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/groves/")
	if path == "" {
		NotFound(w, "Grove")
		return
	}

	// Parse the grove ID (supports both UUID and {uuid}__{slug} format)
	// The grove ID may contain "__" so we need to find the first "/"
	parts := strings.SplitN(path, "/", 2)
	groveIDRaw := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
	}

	// Skip the register endpoint - it's handled separately
	if groveIDRaw == "register" {
		NotFound(w, "Grove")
		return
	}

	// Parse grove ID to extract UUID (supports {uuid}__{slug} format)
	groveID := resolveGroveID(groveIDRaw)

	// Check for nested /agents path
	if strings.HasPrefix(subPath, "agents") {
		agentPath := strings.TrimPrefix(subPath, "agents")
		agentPath = strings.TrimPrefix(agentPath, "/")
		s.handleGroveAgents(w, r, groveID, agentPath)
		return
	}

	// Otherwise handle as grove resource
	s.handleGroveByIDInternal(w, r, groveID, subPath)
}

// handleGroveByIDInternal handles grove resource operations
func (s *Server) handleGroveByIDInternal(w http.ResponseWriter, r *http.Request, groveID, subPath string) {
	// Only handle if no subpath (direct grove resource)
	if subPath != "" {
		NotFound(w, "Grove resource")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getGrove(w, r, groveID)
	case http.MethodPatch:
		s.updateGrove(w, r, groveID)
	case http.MethodDelete:
		s.deleteGrove(w, r, groveID)
	default:
		MethodNotAllowed(w)
	}
}

// handleGroveAgents handles agent operations scoped to a grove
// Path: /api/v1/groves/{groveId}/agents[/{agentId}[/{action}]]
func (s *Server) handleGroveAgents(w http.ResponseWriter, r *http.Request, groveID, agentPath string) {
	ctx := r.Context()

	// Verify grove exists
	grove, err := s.store.GetGrove(ctx, groveID)
	if err != nil {
		if err == store.ErrNotFound {
			NotFound(w, "Grove")
			return
		}
		writeErrorFromErr(w, err, "")
		return
	}

	// No agent ID - list or create agents in this grove
	if agentPath == "" {
		switch r.Method {
		case http.MethodGet:
			s.listGroveAgents(w, r, grove.ID)
		case http.MethodPost:
			s.createGroveAgent(w, r, grove.ID)
		default:
			MethodNotAllowed(w)
		}
		return
	}

	// Parse agent ID and action
	parts := strings.SplitN(agentPath, "/", 2)
	agentIDRaw := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// Handle actions
	if action != "" {
		s.handleGroveAgentAction(w, r, grove.ID, agentIDRaw, action)
		return
	}

	// Handle agent by ID within grove
	switch r.Method {
	case http.MethodGet:
		s.getGroveAgent(w, r, grove.ID, agentIDRaw)
	case http.MethodPatch:
		s.updateGroveAgent(w, r, grove.ID, agentIDRaw)
	case http.MethodDelete:
		s.deleteGroveAgent(w, r, grove.ID, agentIDRaw)
	default:
		MethodNotAllowed(w)
	}
}

// listGroveAgents lists agents within a specific grove
func (s *Server) listGroveAgents(w http.ResponseWriter, r *http.Request, groveID string) {
	ctx := r.Context()
	query := r.URL.Query()

	filter := store.AgentFilter{
		GroveID:       groveID,
		RuntimeHostID: query.Get("runtimeHostId"),
		Status:        query.Get("status"),
	}

	limit := 50
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	result, err := s.store.ListAgents(ctx, filter, store.ListOptions{
		Limit:  limit,
		Cursor: query.Get("cursor"),
	})
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, ListAgentsResponse{
		Agents:     result.Items,
		NextCursor: result.NextCursor,
		TotalCount: result.TotalCount,
	})
}

// createGroveAgent creates an agent within a specific grove
func (s *Server) createGroveAgent(w http.ResponseWriter, r *http.Request, groveID string) {
	ctx := r.Context()

	var req CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Name == "" {
		ValidationError(w, "name is required", nil)
		return
	}

	// Use grove ID from URL path (ignore any groveId in request body)
	// This ensures RESTful consistency

	// Create agent
	agent := &store.Agent{
		ID:         api.NewUUID(),
		AgentID:    api.Slugify(req.Name),
		Name:       req.Name,
		Template:   req.Template,
		GroveID:    groveID,
		Status:     store.AgentStatusPending,
		Labels:     req.Labels,
		Visibility: store.VisibilityPrivate,
	}

	if req.Config != nil {
		agent.Image = req.Config.Image
		if req.Config.Detached != nil {
			agent.Detached = *req.Config.Detached
		} else {
			agent.Detached = true
		}
		agent.AppliedConfig = &store.AgentAppliedConfig{
			Image:   req.Config.Image,
			Env:     req.Config.Env,
			Model:   req.Config.Model,
			Harness: req.Template,
		}
	} else {
		agent.Detached = true
	}

	if err := s.store.CreateAgent(ctx, agent); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	// If a dispatcher is available (co-located runtime host), dispatch the agent
	// This enables zero-friction handoff - the agent will appear in both Hub and Runtime Host
	var warnings []string
	if dispatcher := s.GetDispatcher(); dispatcher != nil {
		if err := dispatcher.DispatchAgentCreate(ctx, agent); err != nil {
			// Log the error but don't fail the request - agent is created in Hub
			warnings = append(warnings, "Failed to dispatch to runtime host: "+err.Error())
			// The agent remains in pending status
		} else {
			// Update agent status to reflect it's being started
			agent.Status = store.AgentStatusProvisioning
			if err := s.store.UpdateAgent(ctx, agent); err != nil {
				warnings = append(warnings, "Failed to update agent status: "+err.Error())
			}
		}
	}

	writeJSON(w, http.StatusCreated, CreateAgentResponse{
		Agent:    agent,
		Warnings: warnings,
	})
}

// getGroveAgent gets an agent by ID within a specific grove
func (s *Server) getGroveAgent(w http.ResponseWriter, r *http.Request, groveID, agentID string) {
	ctx := r.Context()

	// Try to get by slug first (more common case)
	agent, err := s.store.GetAgentBySlug(ctx, groveID, agentID)
	if err != nil {
		if err == store.ErrNotFound {
			// Try by UUID
			agent, err = s.store.GetAgent(ctx, agentID)
			if err != nil {
				writeErrorFromErr(w, err, "")
				return
			}
			// Verify it belongs to this grove
			if agent.GroveID != groveID {
				NotFound(w, "Agent")
				return
			}
		} else {
			writeErrorFromErr(w, err, "")
			return
		}
	}

	writeJSON(w, http.StatusOK, agent)
}

// updateGroveAgent updates an agent within a specific grove
func (s *Server) updateGroveAgent(w http.ResponseWriter, r *http.Request, groveID, agentID string) {
	ctx := r.Context()

	// Try to get by slug first
	agent, err := s.store.GetAgentBySlug(ctx, groveID, agentID)
	if err != nil {
		if err == store.ErrNotFound {
			// Try by UUID
			agent, err = s.store.GetAgent(ctx, agentID)
			if err != nil {
				writeErrorFromErr(w, err, "")
				return
			}
			if agent.GroveID != groveID {
				NotFound(w, "Agent")
				return
			}
		} else {
			writeErrorFromErr(w, err, "")
			return
		}
	}

	var updates struct {
		Name         string            `json:"name,omitempty"`
		Labels       map[string]string `json:"labels,omitempty"`
		Annotations  map[string]string `json:"annotations,omitempty"`
		TaskSummary  string            `json:"taskSummary,omitempty"`
		StateVersion int64             `json:"stateVersion"`
	}

	if err := readJSON(r, &updates); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Check version for optimistic locking
	if updates.StateVersion != 0 && updates.StateVersion != agent.StateVersion {
		Conflict(w, "Version conflict - resource was modified")
		return
	}

	// Apply updates
	if updates.Name != "" {
		agent.Name = updates.Name
	}
	if updates.Labels != nil {
		agent.Labels = updates.Labels
	}
	if updates.Annotations != nil {
		agent.Annotations = updates.Annotations
	}
	if updates.TaskSummary != "" {
		agent.TaskSummary = updates.TaskSummary
	}

	if err := s.store.UpdateAgent(ctx, agent); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// deleteGroveAgent deletes an agent within a specific grove
func (s *Server) deleteGroveAgent(w http.ResponseWriter, r *http.Request, groveID, agentID string) {
	ctx := r.Context()

	// Try to get by slug first to verify grove membership
	agent, err := s.store.GetAgentBySlug(ctx, groveID, agentID)
	if err != nil {
		if err == store.ErrNotFound {
			// Try by UUID
			agent, err = s.store.GetAgent(ctx, agentID)
			if err != nil {
				writeErrorFromErr(w, err, "")
				return
			}
			if agent.GroveID != groveID {
				NotFound(w, "Agent")
				return
			}
		} else {
			writeErrorFromErr(w, err, "")
			return
		}
	}

	if err := s.store.DeleteAgent(ctx, agent.ID); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGroveAgentAction handles actions on agents within a grove
func (s *Server) handleGroveAgentAction(w http.ResponseWriter, r *http.Request, groveID, agentID, action string) {
	if r.Method != http.MethodPost {
		MethodNotAllowed(w)
		return
	}

	ctx := r.Context()

	// Resolve agent ID
	agent, err := s.store.GetAgentBySlug(ctx, groveID, agentID)
	if err != nil {
		if err == store.ErrNotFound {
			agent, err = s.store.GetAgent(ctx, agentID)
			if err != nil {
				writeErrorFromErr(w, err, "")
				return
			}
			if agent.GroveID != groveID {
				NotFound(w, "Agent")
				return
			}
		} else {
			writeErrorFromErr(w, err, "")
			return
		}
	}

	switch action {
	case "status":
		s.updateAgentStatus(w, r, agent.ID)
	case "start", "stop", "restart":
		s.handleAgentLifecycle(w, r, agent.ID, action)
	default:
		NotFound(w, "Action")
	}
}

// resolveGroveID extracts the UUID from a grove ID that may be in {uuid}__{slug} format
func resolveGroveID(groveIDRaw string) string {
	id, _, ok := api.ParseGroveID(groveIDRaw)
	if ok {
		return id
	}
	// Not in hosted format - return as-is (may be just a UUID or slug)
	return groveIDRaw
}

// handleGroveByID is deprecated - use handleGroveRoutes instead
func (s *Server) handleGroveByID(w http.ResponseWriter, r *http.Request) {
	id := extractID(r, "/api/v1/groves")

	if id == "" || id == "register" {
		// Handled by handleGroveRegister
		NotFound(w, "Grove")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getGrove(w, r, id)
	case http.MethodPatch:
		s.updateGrove(w, r, id)
	case http.MethodDelete:
		s.deleteGrove(w, r, id)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) getGrove(w http.ResponseWriter, r *http.Request, id string) {
	grove, err := s.store.GetGrove(r.Context(), id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, grove)
}

func (s *Server) updateGrove(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	grove, err := s.store.GetGrove(ctx, id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	var updates struct {
		Name       string            `json:"name,omitempty"`
		Labels     map[string]string `json:"labels,omitempty"`
		Visibility string            `json:"visibility,omitempty"`
	}

	if err := readJSON(r, &updates); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if updates.Name != "" {
		grove.Name = updates.Name
	}
	if updates.Labels != nil {
		grove.Labels = updates.Labels
	}
	if updates.Visibility != "" {
		grove.Visibility = updates.Visibility
	}

	if err := s.store.UpdateGrove(ctx, grove); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, grove)
}

func (s *Server) deleteGrove(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.store.DeleteGrove(r.Context(), id); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// RuntimeHost Endpoints
// ============================================================================

type ListRuntimeHostsResponse struct {
	Hosts      []store.RuntimeHost `json:"hosts"`
	NextCursor string              `json:"nextCursor,omitempty"`
	TotalCount int                 `json:"totalCount"`
}

func (s *Server) handleRuntimeHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listRuntimeHosts(w, r)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) listRuntimeHosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	filter := store.RuntimeHostFilter{
		Type:    query.Get("type"),
		Status:  query.Get("status"),
		Mode:    query.Get("mode"),
		GroveID: query.Get("groveId"),
	}

	limit := 50
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	result, err := s.store.ListRuntimeHosts(ctx, filter, store.ListOptions{
		Limit:  limit,
		Cursor: query.Get("cursor"),
	})
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, ListRuntimeHostsResponse{
		Hosts:      result.Items,
		NextCursor: result.NextCursor,
		TotalCount: result.TotalCount,
	})
}

func (s *Server) handleRuntimeHostByID(w http.ResponseWriter, r *http.Request) {
	id, action := extractAction(r, "/api/v1/runtime-hosts")

	if id == "" {
		NotFound(w, "RuntimeHost")
		return
	}

	if action == "heartbeat" && r.Method == http.MethodPost {
		s.handleHostHeartbeat(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getRuntimeHost(w, r, id)
	case http.MethodPatch:
		s.updateRuntimeHost(w, r, id)
	case http.MethodDelete:
		s.deleteRuntimeHost(w, r, id)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) getRuntimeHost(w http.ResponseWriter, r *http.Request, id string) {
	host, err := s.store.GetRuntimeHost(r.Context(), id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, host)
}

func (s *Server) updateRuntimeHost(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	host, err := s.store.GetRuntimeHost(ctx, id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	var updates struct {
		Name   string            `json:"name,omitempty"`
		Labels map[string]string `json:"labels,omitempty"`
	}

	if err := readJSON(r, &updates); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if updates.Name != "" {
		host.Name = updates.Name
	}
	if updates.Labels != nil {
		host.Labels = updates.Labels
	}

	if err := s.store.UpdateRuntimeHost(ctx, host); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, host)
}

func (s *Server) deleteRuntimeHost(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.store.DeleteRuntimeHost(r.Context(), id); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHostHeartbeat(w http.ResponseWriter, r *http.Request, id string) {
	var heartbeat struct {
		Status    string              `json:"status"`
		Resources *store.HostResources `json:"resources,omitempty"`
	}

	if err := readJSON(r, &heartbeat); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if err := s.store.UpdateRuntimeHostHeartbeat(r.Context(), id, heartbeat.Status, heartbeat.Resources); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ============================================================================
// Template Endpoints
// ============================================================================

type ListTemplatesResponse struct {
	Templates  []store.Template `json:"templates"`
	NextCursor string           `json:"nextCursor,omitempty"`
	TotalCount int              `json:"totalCount"`
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listTemplates(w, r)
	case http.MethodPost:
		s.createTemplate(w, r)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	filter := store.TemplateFilter{
		Scope:   query.Get("scope"),
		GroveID: query.Get("groveId"),
		Harness: query.Get("harness"),
	}

	limit := 50
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	result, err := s.store.ListTemplates(ctx, filter, store.ListOptions{
		Limit:  limit,
		Cursor: query.Get("cursor"),
	})
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, ListTemplatesResponse{
		Templates:  result.Items,
		NextCursor: result.NextCursor,
		TotalCount: result.TotalCount,
	})
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var template store.Template
	if err := readJSON(r, &template); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if template.Name == "" {
		ValidationError(w, "name is required", nil)
		return
	}
	if template.Harness == "" {
		ValidationError(w, "harness is required", nil)
		return
	}

	template.ID = api.NewUUID()
	template.Slug = api.Slugify(template.Name)

	if template.Scope == "" {
		template.Scope = "global"
	}
	if template.Visibility == "" {
		template.Visibility = store.VisibilityPrivate
	}

	if err := s.store.CreateTemplate(ctx, &template); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusCreated, template)
}

func (s *Server) handleTemplateByID(w http.ResponseWriter, r *http.Request) {
	id := extractID(r, "/api/v1/templates")

	if id == "" {
		NotFound(w, "Template")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getTemplate(w, r, id)
	case http.MethodPut:
		s.updateTemplate(w, r, id)
	case http.MethodDelete:
		s.deleteTemplate(w, r, id)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request, id string) {
	template, err := s.store.GetTemplate(r.Context(), id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, template)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	existing, err := s.store.GetTemplate(ctx, id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	var template store.Template
	if err := readJSON(r, &template); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Preserve ID and timestamps
	template.ID = existing.ID
	template.Created = existing.Created

	if template.Slug == "" {
		template.Slug = api.Slugify(template.Name)
	}

	if err := s.store.UpdateTemplate(ctx, &template); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, template)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.store.DeleteTemplate(r.Context(), id); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// User Endpoints
// ============================================================================

type ListUsersResponse struct {
	Users      []store.User `json:"users"`
	NextCursor string       `json:"nextCursor,omitempty"`
	TotalCount int          `json:"totalCount"`
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listUsers(w, r)
	case http.MethodPost:
		s.createUser(w, r)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	filter := store.UserFilter{
		Role:   query.Get("role"),
		Status: query.Get("status"),
	}

	limit := 50
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	result, err := s.store.ListUsers(ctx, filter, store.ListOptions{
		Limit:  limit,
		Cursor: query.Get("cursor"),
	})
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, ListUsersResponse{
		Users:      result.Items,
		NextCursor: result.NextCursor,
		TotalCount: result.TotalCount,
	})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var user store.User
	if err := readJSON(r, &user); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if user.Email == "" {
		ValidationError(w, "email is required", nil)
		return
	}
	if user.DisplayName == "" {
		ValidationError(w, "displayName is required", nil)
		return
	}

	user.ID = api.NewUUID()
	if user.Role == "" {
		user.Role = store.UserRoleMember
	}
	if user.Status == "" {
		user.Status = "active"
	}

	if err := s.store.CreateUser(ctx, &user); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	id := extractID(r, "/api/v1/users")

	if id == "" {
		NotFound(w, "User")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getUser(w, r, id)
	case http.MethodPatch:
		s.updateUser(w, r, id)
	case http.MethodDelete:
		s.deleteUser(w, r, id)
	default:
		MethodNotAllowed(w)
	}
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request, id string) {
	user, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	user, err := s.store.GetUser(ctx, id)
	if err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	var updates struct {
		DisplayName string                  `json:"displayName,omitempty"`
		Role        string                  `json:"role,omitempty"`
		Status      string                  `json:"status,omitempty"`
		Preferences *store.UserPreferences `json:"preferences,omitempty"`
	}

	if err := readJSON(r, &updates); err != nil {
		BadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if updates.DisplayName != "" {
		user.DisplayName = updates.DisplayName
	}
	if updates.Role != "" {
		user.Role = updates.Role
	}
	if updates.Status != "" {
		user.Status = updates.Status
	}
	if updates.Preferences != nil {
		user.Preferences = updates.Preferences
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		writeErrorFromErr(w, err, "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// Helpers
// ============================================================================

// normalizeGitRemote normalizes a git remote URL for consistent matching.
// Examples:
//   - https://github.com/org/repo.git -> github.com/org/repo
//   - git@github.com:org/repo.git -> github.com/org/repo
func normalizeGitRemote(remote string) string {
	if remote == "" {
		return ""
	}

	// Remove protocol prefix
	remote = strings.TrimPrefix(remote, "https://")
	remote = strings.TrimPrefix(remote, "http://")
	remote = strings.TrimPrefix(remote, "ssh://")
	remote = strings.TrimPrefix(remote, "git://")

	// Handle SSH format (git@host:path)
	if strings.HasPrefix(remote, "git@") {
		remote = strings.TrimPrefix(remote, "git@")
		remote = strings.Replace(remote, ":", "/", 1)
	}

	// Remove .git suffix
	remote = strings.TrimSuffix(remote, ".git")

	// Lowercase the host portion
	if idx := strings.Index(remote, "/"); idx != -1 {
		host := strings.ToLower(remote[:idx])
		path := remote[idx:]
		remote = host + path
	}

	return remote
}
