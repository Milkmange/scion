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
 * Terminal page component
 *
 * Full-screen xterm.js terminal that connects to an agent's tmux session
 * via WebSocket proxy through Koa to the Hub PTY endpoint.
 */

import { LitElement, html, css } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';

import type { PageData, Agent } from '../../shared/types.js';

// xterm.js imports are client-side only — guarded by typeof check in lifecycle
// These will be imported dynamically in firstUpdated() since they require DOM APIs
type Terminal = import('@xterm/xterm').Terminal;
type FitAddon = import('@xterm/addon-fit').FitAddon;

/** PTY WebSocket message types */
interface PTYDataMessage {
  type: 'data';
  data: string; // base64
}

interface PTYResizeMessage {
  type: 'resize';
  cols: number;
  rows: number;
}

type PTYMessage = PTYDataMessage | PTYResizeMessage;

@customElement('scion-page-terminal')
export class ScionPageTerminal extends LitElement {
  @property({ type: Object })
  pageData: PageData | null = null;

  @property({ type: String })
  agentId = '';

  @state()
  private connected = false;

  @state()
  private error: string | null = null;

  @state()
  private agentName = '';

  @state()
  private loading = true;

  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private socket: WebSocket | null = null;
  private resizeObserver: ResizeObserver | null = null;
  private resizeTimer: ReturnType<typeof setTimeout> | null = null;

  static override styles = css`
    :host {
      display: flex;
      flex-direction: column;
      flex: 1;
      min-height: 0;
      background: #1a1a1a;
      color: #eaeaea;
      overflow: hidden;
    }

    .toolbar {
      display: flex;
      align-items: center;
      gap: 0.75rem;
      padding: 0.5rem 1rem;
      background: #141414;
      border-bottom: 1px solid #2a2a2a;
      flex-shrink: 0;
      min-height: 40px;
    }

    .back-link {
      display: inline-flex;
      align-items: center;
      gap: 0.25rem;
      color: #94a3b8;
      text-decoration: none;
      font-size: 0.8125rem;
      white-space: nowrap;
    }

    .back-link:hover {
      color: #60a5fa;
    }

    .separator {
      width: 1px;
      height: 20px;
      background: #2a2a2a;
    }

    .agent-name {
      font-size: 0.875rem;
      font-weight: 500;
      color: #eaeaea;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .spacer {
      flex: 1;
    }

    .status-indicator {
      display: inline-flex;
      align-items: center;
      gap: 0.375rem;
      font-size: 0.75rem;
      color: #94a3b8;
    }

    .status-dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: #ef4444;
    }

    .status-dot.connected {
      background: #22c55e;
    }

    .reconnect-btn {
      background: transparent;
      border: 1px solid #2a2a2a;
      color: #94a3b8;
      padding: 0.25rem 0.75rem;
      border-radius: 4px;
      cursor: pointer;
      font-size: 0.75rem;
    }

    .reconnect-btn:hover {
      border-color: #60a5fa;
      color: #60a5fa;
    }

    .terminal-container {
      flex: 1;
      position: relative;
      overflow: hidden;
    }

    .loading-state,
    .error-state {
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      flex: 1;
      padding: 2rem;
      text-align: center;
    }

    .loading-state p {
      color: #94a3b8;
      margin-top: 1rem;
    }

    .spinner {
      width: 32px;
      height: 32px;
      border: 3px solid #2a2a2a;
      border-top-color: #60a5fa;
      border-radius: 50%;
      animation: spin 0.8s linear infinite;
    }

    @keyframes spin {
      to {
        transform: rotate(360deg);
      }
    }

    .error-state p {
      color: #ef4444;
      margin: 0 0 1rem 0;
    }

    .error-state .error-detail {
      color: #94a3b8;
      font-size: 0.875rem;
      margin-bottom: 1rem;
    }

    .error-state button {
      background: #3b82f6;
      color: #fff;
      border: none;
      padding: 0.5rem 1.5rem;
      border-radius: 6px;
      cursor: pointer;
      font-size: 0.875rem;
    }

    .error-state button:hover {
      background: #2563eb;
    }
  `;

  override connectedCallback(): void {
    super.connectedCallback();
    // SSR property bindings (.agentId=) aren't restored during client-side
    // hydration for top-level page components. Fall back to URL parsing.
    if (!this.agentId && typeof window !== 'undefined') {
      const match = window.location.pathname.match(/\/agents\/([^/]+)/);
      if (match) {
        this.agentId = match[1];
      }
    }
    void this.loadAgentInfo();
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    this.cleanup();
  }

  private async loadAgentInfo(): Promise<void> {
    this.loading = true;
    this.error = null;

    try {
      const response = await fetch(`/api/v1/agents/${this.agentId}`, {
        credentials: 'include',
      });

      if (!response.ok) {
        const errorData = (await response.json().catch(() => ({}))) as { message?: string };
        throw new Error(
          errorData.message || `HTTP ${response.status}: ${response.statusText}`
        );
      }

      const agent = (await response.json()) as Agent;
      this.agentName = agent.name;

      if (agent.status !== 'running') {
        this.error = `Agent is ${agent.status}. Terminal is only available when the agent is running.`;
        this.loading = false;
        return;
      }

      this.loading = false;

      // Wait for render, then initialize terminal
      await this.updateComplete;
      await this.initTerminal();
      this.connectWebSocket();
    } catch (err) {
      console.error('Failed to load agent:', err);
      this.error = err instanceof Error ? err.message : 'Failed to load agent';
      this.loading = false;
    }
  }

  private async initTerminal(): Promise<void> {
    // Dynamic import — xterm.js requires DOM APIs not available during SSR
    const [{ Terminal }, { FitAddon }, { WebLinksAddon }] = await Promise.all([
      import('@xterm/xterm'),
      import('@xterm/addon-fit'),
      import('@xterm/addon-web-links'),
    ]);

    const container = this.shadowRoot?.querySelector('.terminal-container') as HTMLElement;
    if (!container) return;

    this.terminal = new Terminal({
      theme: {
        background: '#1a1a1a',
        foreground: '#eaeaea',
        cursor: '#f39c12',
        cursorAccent: '#1a1a1a',
        selectionBackground: 'rgba(255, 255, 255, 0.3)',
        black: '#1a1a1a',
        red: '#e74c3c',
        green: '#2ecc71',
        yellow: '#f39c12',
        blue: '#3498db',
        magenta: '#9b59b6',
        cyan: '#1abc9c',
        white: '#eaeaea',
        brightBlack: '#546e7a',
        brightRed: '#e57373',
        brightGreen: '#81c784',
        brightYellow: '#ffd54f',
        brightBlue: '#64b5f6',
        brightMagenta: '#ce93d8',
        brightCyan: '#4dd0e1',
        brightWhite: '#ffffff',
      },
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
      fontSize: 14,
      cursorBlink: true,
      cursorStyle: 'block',
      allowProposedApi: true,
    });

    this.fitAddon = new FitAddon();
    this.terminal.loadAddon(this.fitAddon);
    this.terminal.loadAddon(new WebLinksAddon());

    // Inject xterm.css into shadow root
    const xtermStyle = document.createElement('style');
    // We need to fetch and inject xterm CSS since it can't penetrate shadow DOM
    try {
      const cssModule = await import('@xterm/xterm/css/xterm.css?inline');
      xtermStyle.textContent = cssModule.default;
    } catch {
      // Fallback: try to find xterm CSS in bundled assets
      console.warn('[Terminal] Could not load xterm CSS inline, terminal may not render correctly');
    }
    this.shadowRoot?.appendChild(xtermStyle);

    this.terminal.open(container);

    // Defer initial fit until browser has completed layout so the container
    // has its final dimensions (below the toolbar).
    await new Promise((resolve) => requestAnimationFrame(resolve));
    this.fitAddon.fit();

    // Handle terminal input
    this.terminal.onData((data: string) => {
      this.sendData(data);
    });

    this.terminal.onBinary((data: string) => {
      this.sendData(data);
    });

    // Handle terminal resize — fit immediately for visual feedback,
    // debounce the WebSocket resize message to avoid flooding tmux
    this.resizeObserver = new ResizeObserver(() => {
      if (this.fitAddon) {
        this.fitAddon.fit();
        if (this.resizeTimer) clearTimeout(this.resizeTimer);
        this.resizeTimer = setTimeout(() => this.sendResize(), 100);
      }
    });
    this.resizeObserver.observe(container);
  }

  private connectWebSocket(): void {
    if (!this.terminal) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${protocol}//${window.location.host}/api/v1/agents/${this.agentId}/pty?cols=${this.terminal.cols}&rows=${this.terminal.rows}`;

    console.debug('[Terminal] Connecting to', url);
    this.socket = new WebSocket(url);

    this.socket.onopen = () => {
      console.debug('[Terminal] WebSocket connected');
      this.connected = true;
      this.error = null;
      // Re-fit now that the connection is live so tmux gets accurate dimensions
      if (this.fitAddon) {
        this.fitAddon.fit();
        this.sendResize();
      }
      this.terminal?.focus();
    };

    this.socket.onmessage = (event: MessageEvent) => {
      try {
        const raw = event.data;
        if (typeof raw !== 'string') {
          console.warn('[Terminal] Received non-string message frame (binary/Blob), type:', typeof raw, raw);
          return;
        }
        const msg = JSON.parse(raw) as PTYMessage;
        if (msg.type === 'data') {
          const bytes = Uint8Array.from(atob(msg.data), (c) => c.charCodeAt(0));
          this.terminal?.write(bytes);
        }
      } catch (err) {
        console.warn('[Terminal] Failed to parse WebSocket message:', err, event.data);
      }
    };

    this.socket.onclose = (event: CloseEvent) => {
      console.debug('[Terminal] WebSocket closed, code:', event.code, 'reason:', event.reason);
      this.connected = false;
      if (event.code !== 1000) {
        this.error = `Connection closed (code: ${event.code})`;
      }
    };

    this.socket.onerror = (event) => {
      console.error('[Terminal] WebSocket error:', event);
      this.connected = false;
      this.error = 'WebSocket connection error';
    };
  }

  private sendData(data: string): void {
    if (this.socket?.readyState !== WebSocket.OPEN) return;

    // Encode to base64 — handle Unicode properly
    const bytes = new TextEncoder().encode(data);
    const base64 = btoa(String.fromCharCode(...bytes));

    const msg: PTYDataMessage = { type: 'data', data: base64 };
    this.socket.send(JSON.stringify(msg));
  }

  private sendResize(): void {
    if (this.socket?.readyState !== WebSocket.OPEN || !this.terminal) return;

    const msg: PTYResizeMessage = {
      type: 'resize',
      cols: this.terminal.cols,
      rows: this.terminal.rows,
    };
    this.socket.send(JSON.stringify(msg));
  }

  /**
   * Sends a tmux detach sequence (Ctrl-B d) so the tmux client exits cleanly
   * instead of being killed, which would tear down the container.
   */
  private sendTmuxDetach(): void {
    if (this.socket?.readyState === WebSocket.OPEN) {
      // tmux default prefix is Ctrl-B (0x02), detach key is 'd'
      this.sendData('\x02d');
    }
  }

  private cleanup(): void {
    this.sendTmuxDetach();
    if (this.socket) {
      this.socket.close(1000, 'detach');
      this.socket = null;
    }
    if (this.terminal) {
      this.terminal.dispose();
      this.terminal = null;
    }
    if (this.resizeObserver) {
      this.resizeObserver.disconnect();
      this.resizeObserver = null;
    }
    if (this.resizeTimer) {
      clearTimeout(this.resizeTimer);
      this.resizeTimer = null;
    }
    this.fitAddon = null;
  }

  private handleBackToAgent(e: Event): void {
    e.preventDefault();
    this.cleanup();
    window.location.href = `/agents/${this.agentId}`;
  }

  private handleReconnect(): void {
    this.cleanup();
    void this.loadAgentInfo();
  }

  override render() {
    if (this.loading) {
      return html`
        <div class="toolbar">
          <a href="/agents/${this.agentId}" class="back-link">
            &larr; Back to Agent
          </a>
        </div>
        <div class="loading-state">
          <div class="spinner"></div>
          <p>Connecting to agent...</p>
        </div>
      `;
    }

    if (this.error && !this.terminal) {
      return html`
        <div class="toolbar">
          <a href="/agents/${this.agentId}" class="back-link">
            &larr; Back to Agent
          </a>
          ${this.agentName
            ? html`
                <div class="separator"></div>
                <span class="agent-name">${this.agentName}</span>
              `
            : ''}
        </div>
        <div class="error-state">
          <p>Terminal Unavailable</p>
          <div class="error-detail">${this.error}</div>
          <button @click=${() => this.handleReconnect()}>Retry</button>
        </div>
      `;
    }

    return html`
      <div class="toolbar">
        <a href="/agents/${this.agentId}" class="back-link" @click=${(e: Event) => this.handleBackToAgent(e)}>
          &larr; Back to Agent
        </a>
        <div class="separator"></div>
        <span class="agent-name">${this.agentName || this.agentId}</span>
        <div class="spacer"></div>
        <div class="status-indicator">
          <span class="status-dot ${this.connected ? 'connected' : ''}"></span>
          ${this.connected ? 'Connected' : 'Disconnected'}
        </div>
        ${!this.connected
          ? html`
              <button class="reconnect-btn" @click=${() => this.handleReconnect()}>
                Reconnect
              </button>
            `
          : ''}
      </div>
      ${this.error
        ? html`
            <div
              style="padding: 0.375rem 1rem; background: #7f1d1d; color: #fecaca; font-size: 0.75rem;"
            >
              ${this.error}
            </div>
          `
        : ''}
      <div class="terminal-container"></div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'scion-page-terminal': ScionPageTerminal;
  }
}
