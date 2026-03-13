/**
 * Copyright 2026 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/**
 * Grove settings page component
 *
 * Displays grove-scoped environment variables, secrets, and danger-zone actions (delete).
 */

import { LitElement, html, css } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';

import type { PageData, Grove } from '../../shared/types.js';
import { can, canAny } from '../../shared/types.js';
import { apiFetch } from '../../client/api.js';
import '../shared/env-var-list.js';
import '../shared/secret-list.js';
import '../shared/shared-dir-list.js';

@customElement('scion-page-grove-settings')
export class ScionPageGroveSettings extends LitElement {
  @property({ type: Object })
  pageData: PageData | null = null;

  @property({ type: String })
  groveId = '';

  @state()
  private loading = true;

  @state()
  private grove: Grove | null = null;

  @state()
  private error: string | null = null;

  @state()
  private deleteLoading = false;

  @state()
  private deleteAlsoAgents = false;

  static override styles = css`
    :host {
      display: block;
    }

    .back-link {
      display: inline-flex;
      align-items: center;
      gap: 0.5rem;
      color: var(--scion-text-muted, #64748b);
      text-decoration: none;
      font-size: 0.875rem;
      margin-bottom: 1rem;
    }

    .back-link:hover {
      color: var(--scion-primary, #3b82f6);
    }

    .header {
      display: flex;
      align-items: center;
      gap: 0.75rem;
      margin-bottom: 2rem;
    }

    .header sl-icon {
      color: var(--scion-primary, #3b82f6);
      font-size: 1.5rem;
    }

    .header h1 {
      font-size: 1.5rem;
      font-weight: 700;
      color: var(--scion-text, #1e293b);
      margin: 0;
    }

    .section {
      background: var(--scion-surface, #ffffff);
      border: 1px solid var(--scion-border, #e2e8f0);
      border-radius: var(--scion-radius-lg, 0.75rem);
      padding: 1.5rem;
      margin-bottom: 1.5rem;
    }

    .section h2 {
      font-size: 1.125rem;
      font-weight: 600;
      color: var(--scion-text, #1e293b);
      margin: 0 0 0.25rem 0;
    }

    .section p {
      font-size: 0.875rem;
      color: var(--scion-text-muted, #64748b);
      margin: 0 0 1rem 0;
    }

    .danger-section {
      border-color: var(--sl-color-danger-200, #fecaca);
    }

    .danger-section h2 {
      color: var(--sl-color-danger-600, #dc2626);
    }

    .delete-area {
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      gap: 1.5rem;
      padding-top: 1rem;
      border-top: 1px solid var(--scion-border, #e2e8f0);
    }

    .delete-info {
      flex: 1;
    }

    .delete-info h3 {
      font-size: 0.9375rem;
      font-weight: 600;
      color: var(--scion-text, #1e293b);
      margin: 0 0 0.25rem 0;
    }

    .delete-info p {
      font-size: 0.8125rem;
      color: var(--scion-text-muted, #64748b);
      margin: 0;
    }

    .delete-actions {
      flex-shrink: 0;
      display: flex;
      flex-direction: column;
      align-items: flex-end;
      gap: 0.75rem;
    }

    .checkbox-label {
      display: flex;
      align-items: center;
      gap: 0.5rem;
      font-size: 0.8125rem;
      color: var(--scion-text-muted, #64748b);
      cursor: pointer;
      user-select: none;
    }

    .checkbox-label input[type="checkbox"] {
      cursor: pointer;
    }

    .loading-state {
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      padding: 4rem 2rem;
      color: var(--scion-text-muted, #64748b);
    }

    .loading-state sl-spinner {
      font-size: 2rem;
      margin-bottom: 1rem;
    }

    .error-state {
      text-align: center;
      padding: 3rem 2rem;
      background: var(--scion-surface, #ffffff);
      border: 1px solid var(--sl-color-danger-200, #fecaca);
      border-radius: var(--scion-radius-lg, 0.75rem);
    }

    .error-state sl-icon {
      font-size: 3rem;
      color: var(--sl-color-danger-500, #ef4444);
      margin-bottom: 1rem;
    }

    .error-state h2 {
      font-size: 1.25rem;
      font-weight: 600;
      color: var(--scion-text, #1e293b);
      margin: 0 0 0.5rem 0;
    }

    .error-state p {
      color: var(--scion-text-muted, #64748b);
      margin: 0 0 1rem 0;
    }

    .error-details {
      font-family: var(--scion-font-mono, monospace);
      font-size: 0.875rem;
      background: var(--scion-bg-subtle, #f1f5f9);
      padding: 0.75rem 1rem;
      border-radius: var(--scion-radius, 0.5rem);
      color: var(--sl-color-danger-700, #b91c1c);
      margin-bottom: 1rem;
    }
  `;

  override connectedCallback(): void {
    super.connectedCallback();
    if (!this.groveId && typeof window !== 'undefined') {
      const match = window.location.pathname.match(/\/groves\/([^/]+)/);
      if (match) {
        this.groveId = match[1];
      }
    }
    void this.loadGrove();
  }

  private async loadGrove(): Promise<void> {
    this.loading = true;
    this.error = null;

    try {
      const response = await apiFetch(`/api/v1/groves/${this.groveId}`);

      if (!response.ok) {
        const errorData = (await response.json().catch(() => ({}))) as { message?: string };
        throw new Error(
          errorData.message || `HTTP ${response.status}: ${response.statusText}`
        );
      }

      this.grove = (await response.json()) as Grove;
    } catch (err) {
      console.error('Failed to load grove:', err);
      this.error = err instanceof Error ? err.message : 'Failed to load grove';
    } finally {
      this.loading = false;
    }
  }

  private async handleDeleteGrove(event?: MouseEvent): Promise<void> {
    const groveName = this.grove?.name || this.groveId;
    const agentWarning = this.deleteAlsoAgents
      ? '\n\nThis will also delete all agents in this grove.'
      : '';

    if (!event?.altKey && !confirm(`Are you sure you want to delete "${groveName}"?${agentWarning}\n\nThis action cannot be undone.`)) {
      return;
    }

    this.deleteLoading = true;

    try {
      const params = this.deleteAlsoAgents ? '?deleteAgents=true' : '';
      const response = await apiFetch(`/api/v1/groves/${this.groveId}${params}`, {
        method: 'DELETE',
      });

      if (!response.ok && response.status !== 204) {
        const errorData = (await response.json().catch(() => ({}))) as { message?: string };
        throw new Error(errorData.message || `Failed to delete grove: HTTP ${response.status}`);
      }

      // Navigate back to groves list
      window.history.pushState({}, '', '/groves');
      window.dispatchEvent(new PopStateEvent('popstate'));
    } catch (err) {
      console.error('Failed to delete grove:', err);
      alert(err instanceof Error ? err.message : 'Failed to delete grove');
    } finally {
      this.deleteLoading = false;
    }
  }

  override render() {
    if (this.loading) {
      return this.renderLoading();
    }

    if (this.error || !this.grove) {
      return this.renderError();
    }

    return html`
      <a href="/groves/${this.groveId}" class="back-link">
        <sl-icon name="arrow-left"></sl-icon>
        Back to Grove
      </a>

      <div class="header">
        <sl-icon name="gear"></sl-icon>
        <h1>${this.grove.name} Settings</h1>
      </div>

      ${canAny(this.grove._capabilities, 'update', 'manage') ? html`
        <scion-env-var-list
          scope="grove"
          scopeId=${this.groveId}
          apiBasePath="/api/v1/groves/${this.groveId}"
          compact
        ></scion-env-var-list>

        <scion-secret-list
          scope="grove"
          scopeId=${this.groveId}
          apiBasePath="/api/v1/groves/${this.groveId}"
          compact
        ></scion-secret-list>

        <scion-shared-dir-list
          groveId=${this.groveId}
          apiBasePath="/api/v1/groves/${this.groveId}"
        ></scion-shared-dir-list>
      ` : ''}

      ${can(this.grove._capabilities, 'delete') ? html`
        <div class="section danger-section">
          <h2>Danger Zone</h2>
          <p>Irreversible actions that affect this grove and its resources.</p>

          <div class="delete-area">
            <div class="delete-info">
              <h3>Delete this grove</h3>
              <p>
                Permanently remove this grove and its configuration.
                This action cannot be undone.
              </p>
            </div>
            <div class="delete-actions">
              <label class="checkbox-label">
                <input
                  type="checkbox"
                  .checked=${this.deleteAlsoAgents}
                  @change=${(e: Event) => {
                    this.deleteAlsoAgents = (e.target as HTMLInputElement).checked;
                  }}
                />
                Also delete all agents
              </label>
              <sl-button
                variant="danger"
                size="small"
                ?loading=${this.deleteLoading}
                ?disabled=${this.deleteLoading}
                @click=${(e: MouseEvent) => this.handleDeleteGrove(e)}
              >
                <sl-icon slot="prefix" name="trash"></sl-icon>
                Delete Grove
              </sl-button>
            </div>
          </div>
        </div>
      ` : html`
        <div class="section">
          <h2>Permissions</h2>
          <p>You don't have permission to modify this grove.</p>
        </div>
      `}
    `;
  }

  private renderLoading() {
    return html`
      <div class="loading-state">
        <sl-spinner></sl-spinner>
        <p>Loading settings...</p>
      </div>
    `;
  }

  private renderError() {
    return html`
      <a href="/groves/${this.groveId}" class="back-link">
        <sl-icon name="arrow-left"></sl-icon>
        Back to Grove
      </a>

      <div class="error-state">
        <sl-icon name="exclamation-triangle"></sl-icon>
        <h2>Failed to Load Settings</h2>
        <p>There was a problem loading this grove.</p>
        <div class="error-details">${this.error || 'Grove not found'}</div>
        <sl-button variant="primary" @click=${() => this.loadGrove()}>
          <sl-icon slot="prefix" name="arrow-clockwise"></sl-icon>
          Retry
        </sl-button>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'scion-page-grove-settings': ScionPageGroveSettings;
  }
}
