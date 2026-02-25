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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ptone/scion-agent/pkg/store"
	"github.com/ptone/scion-agent/pkg/store/sqlite"
)

// testTemplateBootstrapServer creates a hub Server backed by an in-memory
// SQLite store and a mock storage, suitable for template bootstrap tests.
func testTemplateBootstrapServer(t *testing.T) (*Server, store.Store, *mockStorage) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		if strings.Contains(err.Error(), "sqlite driver not registered") {
			t.Skip("Skipping: sqlite driver not registered")
		}
		t.Fatalf("failed to create test store: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	cfg := DefaultServerConfig()
	srv := New(cfg, s)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	stor := newMockStorage("test-bucket")
	srv.SetStorage(stor)

	return srv, s, stor
}

// makeTemplateDir creates a temp directory with template files and returns
// the parent templates directory. The template is created as a subdirectory
// named templateName.
func makeTemplateDir(t *testing.T, templateName string, files map[string]string) string {
	t.Helper()
	templatesDir := t.TempDir()
	templateDir := filepath.Join(templatesDir, templateName)
	for relPath, content := range files {
		full := filepath.Join(templateDir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return templatesDir
}

func TestBootstrapTemplatesFromDir_ImportsTemplates(t *testing.T) {
	srv, s, stor := testTemplateBootstrapServer(t)
	ctx := context.Background()

	templatesDir := makeTemplateDir(t, "my-template", map[string]string{
		"home/.bashrc": "# bashrc content",
		"README.md":    "hello",
	})

	if err := srv.BootstrapTemplatesFromDir(ctx, templatesDir); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	// Verify a template was created in the store
	result, err := s.ListTemplates(ctx, store.TemplateFilter{}, store.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Fatalf("expected 1 template, got %d", result.TotalCount)
	}

	tmpl := result.Items[0]
	if tmpl.Name != "my-template" {
		t.Errorf("expected name 'my-template', got %q", tmpl.Name)
	}
	if tmpl.Status != store.TemplateStatusActive {
		t.Errorf("expected status active, got %q", tmpl.Status)
	}
	if tmpl.Scope != store.TemplateScopeGlobal {
		t.Errorf("expected scope global, got %q", tmpl.Scope)
	}
	if len(tmpl.Files) != 2 {
		t.Errorf("expected 2 files in manifest, got %d", len(tmpl.Files))
	}
	if tmpl.ContentHash == "" {
		t.Error("expected non-empty content hash")
	}

	// Verify files were uploaded to storage
	if len(stor.objects) != 2 {
		t.Errorf("expected 2 objects in storage, got %d", len(stor.objects))
	}
}

func TestBootstrapTemplatesFromDir_NoopWhenTemplatesExist(t *testing.T) {
	srv, s, stor := testTemplateBootstrapServer(t)
	ctx := context.Background()

	// Pre-create a template in the store
	existing := &store.Template{
		ID:     "existing-id",
		Name:   "existing",
		Slug:   "existing",
		Scope:  store.TemplateScopeGlobal,
		Status: store.TemplateStatusActive,
	}
	if err := s.CreateTemplate(ctx, existing); err != nil {
		t.Fatal(err)
	}

	templatesDir := makeTemplateDir(t, "new-template", map[string]string{
		"file.txt": "content",
	})

	if err := srv.BootstrapTemplatesFromDir(ctx, templatesDir); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	// Verify no new templates were created
	result, err := s.ListTemplates(ctx, store.TemplateFilter{}, store.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Fatalf("expected 1 template (existing only), got %d", result.TotalCount)
	}
	if result.Items[0].Name != "existing" {
		t.Errorf("expected existing template, got %q", result.Items[0].Name)
	}

	// Verify nothing was uploaded to storage
	if len(stor.objects) != 0 {
		t.Errorf("expected 0 objects in storage, got %d", len(stor.objects))
	}
}

func TestBootstrapTemplatesFromDir_NoopWhenNoStorage(t *testing.T) {
	// Create server without storage
	s, err := sqlite.New(":memory:")
	if err != nil {
		if strings.Contains(err.Error(), "sqlite driver not registered") {
			t.Skip("Skipping: sqlite driver not registered")
		}
		t.Fatalf("failed to create test store: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	cfg := DefaultServerConfig()
	srv := New(cfg, s)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	// Deliberately not calling srv.SetStorage()

	ctx := context.Background()
	templatesDir := makeTemplateDir(t, "some-template", map[string]string{
		"file.txt": "content",
	})

	// Should not error, just skip
	if err := srv.BootstrapTemplatesFromDir(ctx, templatesDir); err != nil {
		t.Fatalf("bootstrap should not fail without storage: %v", err)
	}

	// Verify no templates were created
	result, err := s.ListTemplates(ctx, store.TemplateFilter{}, store.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 0 {
		t.Fatalf("expected 0 templates, got %d", result.TotalCount)
	}
}

func TestBootstrapTemplatesFromDir_EmptyDirectory(t *testing.T) {
	srv, s, _ := testTemplateBootstrapServer(t)
	ctx := context.Background()

	// Create an empty templates directory
	templatesDir := t.TempDir()

	if err := srv.BootstrapTemplatesFromDir(ctx, templatesDir); err != nil {
		t.Fatalf("bootstrap failed on empty dir: %v", err)
	}

	result, err := s.ListTemplates(ctx, store.TemplateFilter{}, store.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 0 {
		t.Fatalf("expected 0 templates, got %d", result.TotalCount)
	}
}

func TestBootstrapTemplatesFromDir_NonexistentDirectory(t *testing.T) {
	srv, _, _ := testTemplateBootstrapServer(t)
	ctx := context.Background()

	if err := srv.BootstrapTemplatesFromDir(ctx, "/nonexistent/path"); err != nil {
		t.Fatalf("bootstrap should silently skip nonexistent directory: %v", err)
	}
}

func TestBootstrapTemplatesFromDir_MultipleTemplates(t *testing.T) {
	srv, s, _ := testTemplateBootstrapServer(t)
	ctx := context.Background()

	templatesDir := t.TempDir()

	// Create two template subdirectories
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(templatesDir, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.txt"), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := srv.BootstrapTemplatesFromDir(ctx, templatesDir); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	result, err := s.ListTemplates(ctx, store.TemplateFilter{}, store.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 2 {
		t.Fatalf("expected 2 templates, got %d", result.TotalCount)
	}
}

func TestBootstrapTemplatesFromDir_SkipsNonDirectories(t *testing.T) {
	srv, s, _ := testTemplateBootstrapServer(t)
	ctx := context.Background()

	templatesDir := t.TempDir()

	// Create a regular file (not a directory) at the top level
	if err := os.WriteFile(filepath.Join(templatesDir, "not-a-template.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create one valid template
	dir := filepath.Join(templatesDir, "valid")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := srv.BootstrapTemplatesFromDir(ctx, templatesDir); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	result, err := s.ListTemplates(ctx, store.TemplateFilter{}, store.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Fatalf("expected 1 template (skipping file), got %d", result.TotalCount)
	}
}

func TestDetectHarnessFromConfig_NameBased(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"claude", "claude"},
		{"my-claude-template", "claude"},
		{"gemini", "gemini"},
		{"custom-gemini-pro", "gemini"},
		{"opencode", "opencode"},
		{"codex", "codex"},
		{"default", ""},
		{"my-custom", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use an empty temp dir so config loading returns empty config
			dir := t.TempDir()
			got := detectHarnessFromConfig(dir, tt.name)
			if got != tt.expected {
				t.Errorf("detectHarnessFromConfig(%q, %q) = %q, want %q", dir, tt.name, got, tt.expected)
			}
		})
	}
}

func TestDetectHarnessFromConfig_FromConfigFile(t *testing.T) {
	dir := t.TempDir()

	// Write a scion-agent.yaml with a harness_config field
	configContent := `harness_config: claude
`
	if err := os.WriteFile(filepath.Join(dir, "scion-agent.yaml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	got := detectHarnessFromConfig(dir, "my-template")
	if got != "claude" {
		t.Errorf("expected 'claude' from config, got %q", got)
	}
}

func TestDetectHarnessFromConfig_DefaultHarnessConfig(t *testing.T) {
	dir := t.TempDir()

	configContent := `default_harness_config: gemini
`
	if err := os.WriteFile(filepath.Join(dir, "scion-agent.yaml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	got := detectHarnessFromConfig(dir, "my-template")
	if got != "gemini" {
		t.Errorf("expected 'gemini' from config, got %q", got)
	}
}

func TestDetectHarnessFromConfig_HarnessField(t *testing.T) {
	dir := t.TempDir()

	configContent := `harness: codex
`
	if err := os.WriteFile(filepath.Join(dir, "scion-agent.yaml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	got := detectHarnessFromConfig(dir, "my-template")
	if got != "codex" {
		t.Errorf("expected 'codex' from config, got %q", got)
	}
}
