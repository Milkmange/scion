# GitHub App QA Walkthrough: Grove Owner (Non-Admin User)

**Created:** 2026-03-19
**Prerequisites:** A running Scion Hub with GitHub App already configured by an admin, a GitHub org/user account where you can install apps, CLI authenticated to the Hub

---

## Overview

As a grove owner (non-admin), you don't configure the GitHub App itself — that's the admin's job. Your workflow is:

1. Install the Hub's GitHub App on your org/account
2. Create a grove pointing to your repo
3. Verify the grove is linked to the installation
4. Run agents that automatically get short-lived GitHub tokens

---

## Part 1: Install the GitHub App

### 1.1 Get the Install Link

Ask your Hub admin for the GitHub App installation URL. It will look like:

```
https://github.com/apps/<app-slug>/installations/new
```

Alternatively, the grove settings page in the Web UI may show an "Install GitHub App" button if no installation is detected.

### 1.2 Install on Your Org or Account

1. Click the install link.
2. Select your **organization** or **personal account**.
3. Choose repository access:
   - **All repositories** — the app can access everything (simplest if you trust it)
   - **Only select repositories** — pick the specific repos your groves will use
4. Click **Install**.
5. GitHub redirects to the Hub's setup callback page. You should see a confirmation showing:
   - Your org/user account name
   - The repos granted
   - Any groves that were auto-matched

### 1.3 Verify on the Hub

```bash
HUB_URL=https://your-hub.example.com
TOKEN=your-user-token

# List installations (you should see yours)
curl -s "$HUB_URL/api/v1/github-app/installations" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Look for your `account_login` in the results with `status: "active"`.

---

## Part 2: Create and Link a Grove

### 2.1 Create a Grove via CLI

```bash
scion hub grove create my-project --git-remote https://github.com/your-org/your-repo.git
```

Or via API:

```bash
curl -s -X POST "$HUB_URL/api/v1/groves" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-project",
    "git_remote": "https://github.com/your-org/your-repo.git"
  }' | jq .
```

Note the grove `id` from the response.

### 2.2 Check Auto-Association

If the app was already installed when you created the grove, it may have been auto-matched. Check:

```bash
GROVE_ID=your-grove-id

curl -s "$HUB_URL/api/v1/groves/$GROVE_ID/github-status" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

**If auto-matched:** You'll see `installation_id` set and `state: "unchecked"`. Skip to Part 3.

**If not matched:** `installation_id` will be `null`. Continue to 2.3.

### 2.3 Trigger Discovery (if not auto-matched)

```bash
curl -s -X POST "$HUB_URL/api/v1/github-app/installations/discover" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Look for your grove ID in the `matched_groves` arrays. Then re-check:

```bash
curl -s "$HUB_URL/api/v1/groves/$GROVE_ID/github-status" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 2.4 Manual Association (Fallback)

If discovery doesn't match (e.g., the git remote URL format doesn't match), associate manually:

```bash
# Find your installation ID from the installations list
INSTALLATION_ID=12345

curl -s -X PUT "$HUB_URL/api/v1/groves/$GROVE_ID/github-installation" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"installation_id\": $INSTALLATION_ID}" | jq .
```

**Expected:**
```json
{
  "grove_id": "your-grove-id",
  "installation_id": 12345,
  "status": "associated"
}
```

---

## Part 3: Run an Agent

### 3.1 Start the Agent

```bash
scion start --grove my-project my-agent
```

The Hub will:
1. Detect the grove has a GitHub App installation
2. Mint a short-lived installation token (1-hour expiry)
3. Inject it as `GITHUB_TOKEN` in the agent's environment

### 3.2 Verify Inside the Agent

```bash
scion attach my-agent

# Inside the agent container:
echo $GITHUB_TOKEN              # Should start with "ghs_"
echo $SCION_GITHUB_APP_ENABLED  # Should be "true"

# Test GitHub access
gh auth status
git ls-remote origin
gh repo view
```

All of these should work using the automatically minted token.

### 3.3 Verify Grove Status Updated

After the agent starts and the token is successfully minted:

```bash
curl -s "$HUB_URL/api/v1/groves/$GROVE_ID/github-status" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

**Expected:**
```json
{
  "grove_id": "your-grove-id",
  "installation_id": 12345,
  "status": {
    "state": "ok",
    "last_token_mint": "2026-03-19T...",
    "last_checked": "2026-03-19T..."
  }
}
```

---

## Part 4: Token Refresh (Long-Running Agents)

If your agent runs for more than 1 hour, the token is automatically refreshed:

- **Git operations**: The credential helper transparently gets a fresh token on each git command
- **`gh` CLI**: A wrapper reads the latest token from the token file before each invocation
- **Background loop**: `sciontool` proactively refreshes the token ~10 minutes before expiry

You don't need to do anything — this is fully automatic. To verify it's working:

```bash
# Inside the agent (after >50 minutes):
cat $SCION_GITHUB_TOKEN_PATH    # Should show a current token
gh auth status                  # Should still be authenticated
```

---

## Part 5: Customize Permissions (Optional)

By default, the token is minted with `contents:write`, `pull_requests:write`, `metadata:read`. If your agents need different permissions:

### 5.1 View Current Permissions

```bash
curl -s "$HUB_URL/api/v1/groves/$GROVE_ID/github-permissions" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 5.2 Add Issue Permissions

```bash
curl -s -X PUT "$HUB_URL/api/v1/groves/$GROVE_ID/github-permissions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": "write",
    "pull_requests": "write",
    "issues": "write",
    "metadata": "read"
  }' | jq .
```

### 5.3 Read-Only Mode

For groves that should only read code (no push):

```bash
curl -s -X PUT "$HUB_URL/api/v1/groves/$GROVE_ID/github-permissions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": "read",
    "metadata": "read"
  }' | jq .
```

---

## Part 6: Configure Commit Attribution (Optional)

### 6.1 View Current Mode

```bash
curl -s "$HUB_URL/api/v1/groves/$GROVE_ID/git-identity" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Default is `bot` mode — commits appear from the app's bot identity.

### 6.2 Use Custom Author

```bash
curl -s -X PUT "$HUB_URL/api/v1/groves/$GROVE_ID/git-identity" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "mode": "custom",
    "name": "Project Bot",
    "email": "bot@your-org.com"
  }' | jq .
```

### 6.3 Co-authored Commits

Commits use the bot identity but include a `Co-authored-by` trailer linking to the user who started the agent:

```bash
curl -s -X PUT "$HUB_URL/api/v1/groves/$GROVE_ID/git-identity" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "co-authored"}' | jq .
```

---

## Part 7: Check the Web UI

### 7.1 Grove Settings Page

Navigate to your grove's settings in the Web UI. The **GitHub App** section should show:

| State | What You See |
|-------|-------------|
| No installation | "No GitHub App installed." + install link |
| `unchecked` | Neutral indicator: "Status will be verified on next agent start." |
| `ok` | Green dot: "GitHub App active." + last token mint time |
| `degraded` | Yellow warning with the issue and remediation link |
| `error` | Red error with the issue, remediation steps, and PAT fallback note if applicable |

### 7.2 Permission Badges

The grove settings should show the configured permissions as badges (e.g., `contents:write`, `pull_requests:write`).

---

## Part 8: Troubleshooting

### 8.1 Grove Shows "error" State

Check the specific error:

```bash
curl -s "$HUB_URL/api/v1/groves/$GROVE_ID/github-status" \
  -H "Authorization: Bearer $TOKEN" | jq .status
```

| Error Code | Meaning | Fix |
|------------|---------|-----|
| `installation_revoked` | App was uninstalled from your org | Reinstall the app |
| `installation_suspended` | Org admin suspended the app | Contact your org admin |
| `repo_not_accessible` | Repo was removed from the app's access | Add the repo back in GitHub App installation settings |
| `permission_denied` | Grove requests permissions the app doesn't have | Reduce grove permissions or ask Hub admin to add permissions to the app |
| `token_mint_failed` | Transient failure (network, GitHub outage) | Will auto-clear on next successful mint |

### 8.2 Agent Falls Back to PAT

If the GitHub App has an issue but you have a `GITHUB_TOKEN` secret configured on the grove, the agent will use the PAT as a fallback. The Web UI will show: "Using PAT fallback. GitHub App issue: {error message}."

### 8.3 Token Not Working Inside Agent

1. Check the token format: `echo $GITHUB_TOKEN` — should start with `ghs_`
2. Check `gh auth status` for detailed error
3. Verify the repo is in the installation's repo list
4. Check if the token has the needed permissions for your operation

### 8.4 Grove Not Auto-Matching

Auto-matching works by comparing the grove's `git_remote` URL against the installation's repo list. Common reasons it fails:
- Git remote uses SSH format (`git@github.com:org/repo.git`) but matching expects HTTPS (both should work, but verify)
- Grove was created before the app was installed (run discovery)
- Repo name has a typo

---

## Part 9: Removing GitHub App from a Grove

### 9.1 Disassociate (Keep Installation)

This removes the GitHub App link from the grove but keeps the installation record:

```bash
curl -s -X DELETE "$HUB_URL/api/v1/groves/$GROVE_ID/github-installation" \
  -H "Authorization: Bearer $TOKEN"
```

The grove will fall back to PAT-based auth for future agents.

### 9.2 Uninstall the App (on GitHub)

If you want to fully remove the app from your org:

1. Go to **GitHub > Org Settings > Installed GitHub Apps**
2. Click **Configure** next to the Scion app
3. Click **Uninstall**

The Hub will receive a webhook and mark all affected groves with `installation_revoked`.

---

## Checklist Summary

| # | Test | Pass |
|---|------|------|
| 1 | Install app on GitHub org/account, setup callback returns repos | |
| 2 | Installation appears in Hub installations list | |
| 3 | Create a grove with git remote matching an installed repo | |
| 4 | Grove auto-matched (or matched via discover/manual) | |
| 5 | Grove status shows `unchecked` before agent run | |
| 6 | Agent starts with `ghs_` token and `SCION_GITHUB_APP_ENABLED=true` | |
| 7 | `gh auth status` and `git ls-remote` work inside agent | |
| 8 | Grove status becomes `ok` with `last_token_mint` timestamp | |
| 9 | Custom permissions save and apply correctly | |
| 10 | Git identity mode changes persist | |
| 11 | Web UI shows correct status indicator for the grove | |
| 12 | Disassociating grove clears installation link and status | |
