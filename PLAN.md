# Deploy CLI — Implementation Plan

## Overview

A Go-based deployment CLI (`deploycli`) that supports human-interactive use (via GitLab SSO), agent/skill automation (via tokens), diff-based deployment confirmation, and CI-mode auto-approval. 
Designed from the ground up to be reusable as both a standalone tool and as an MCP/agent skill.

---

## Technology

- GoLang 1.26
- GitLab https://gitlab.com/gitlab-org/api/client-go library usage

## Goals

| Goal | Description |
|---|---|
| Multi-command | Deploy application, deploy API, list versions |
| Dual auth | GitLab OAuth (human) + token (agent/CI) |
| Diff-aware | Show changelog between current and target version before deploying |
| Approval gate | Interactive confirmation for humans; auto-approve in CI/agent mode |
| Agent/skill friendly | Machine-readable output, structured exit codes, JSON mode |

---

## Architecture

```
deploycli/
├── cmd/                        # Cobra command definitions
│   ├── root.go                 # Root command, global flags, auth bootstrap
│   ├── deploy_app.go           # deploy app
│   ├── deploy_api.go           # deploy api
│   └── versions.go             # versions list
├── internal/
│   ├── auth/
│   │   ├── auth.go             # Auth interface
│   │   ├── oauth.go            # GitLab OAuth PKCE flow
│   │   ├── token.go            # Static token (agent/CI mode)
│   │   └── keychain.go         # Secure token storage (OS keychain)
│   ├── client/
│   │   ├── client.go           # Deployment API HTTP client
│   │   ├── versions.go         # Version list/fetch calls
│   │   └── deploy.go           # Deploy calls
│   ├── diff/
│   │   ├── diff.go             # Diff engine: compare versions
│   │   └── render.go           # Human-readable + JSON diff output
│   ├── approval/
│   │   └── approval.go         # Confirm prompt + CI/agent bypass
│   └── output/
│       ├── output.go           # Output interface (text/JSON)
│       └── table.go            # Table rendering for version lists
├── pkg/
│   └── skill/
│       └── skill.go            # Skill-friendly wrapper (JSON-in, JSON-out)
├── config/
│   └── config.go               # Config file (~/.deploycli/config.yaml)
├── main.go
└── go.mod
```

---

## Authentication Design

### Two Auth Modes

```
                    ┌─────────────────┐
                    │   Auth Mode?     │
                    └────────┬────────┘
                             │
           ┌─────────────────┴──────────────────┐
           │                                    │
    Human / Skill                         Agent / CI
  (interactive TTY)                   (non-interactive)
           │                                    │
    GitLab OAuth PKCE              Token from env / flag / config
    (browser redirect              DEPLOYCLI_TOKEN or --token
    or device flow)                             │
           │                                    │
    Token cached in                     Token used directly
    OS keychain                         in HTTP requests
```

### GitLab OAuth PKCE Flow (Human / Skill Mode)

1. CLI generates a PKCE code verifier + challenge
2. Opens `https://gitlab.yourcompany.com/oauth/authorize?...` in the browser (or prints the URL for headless environments)
3. Starts a local callback server on a random port (e.g. `http://localhost:PORT/callback`)
4. GitLab redirects back with an authorization code
5. CLI exchanges the code + verifier for an access token
6. Token is stored in the OS keychain (`deploycli/token`) via `zalando/go-keyring`
7. Subsequent commands skip the OAuth flow if a valid token is found in keychain

### Token Mode (Agent / CI)

- Source priority: `--token` flag → `DEPLOYCLI_TOKEN` env var → config file
- No browser, no keychain, no user interaction
- Detected automatically when a TTY is not present or `--ci` / `--agent` flag is set

### Auth Interface

```go
type Authenticator interface {
    GetToken(ctx context.Context) (string, error)
    Refresh(ctx context.Context) error
    Logout() error
}
```

---

## Command Design

### Global Flags

```
--env           string   Target environment (required: staging, production, ...)
--output        string   Output format: text (default) | json
--token         string   Auth token (agent/CI mode)
--ci                     Enable CI mode (auto-approve, structured output)
--agent                  Enable agent mode (auto-approve, JSON output)
--config        string   Path to config file (default: ~/.deploycli/config.yaml)
```

### `deploy app`

```
deploycli deploy app --env staging --version v1.4.2
```

**Flow:**
1. Authenticate
2. Fetch current deployed version for `--env`
3. Fetch metadata for target `--version`
4. Compute and render diff summary
5. Prompt for approval (skip in CI/agent mode)
6. Execute deployment
7. Poll for deployment status and stream progress
8. Exit 0 on success, non-zero on failure

### `deploy api`

```
deploycli deploy api --env production --version v2.1.0
```

Same flow as `deploy app` but targets the API service. Internally calls a different deployment endpoint.

### `versions list`

```
deploycli versions list --env staging --service app
deploycli versions list --env staging --service api --output json
```

**Output (text):**
```
VERSION     DEPLOYED    STATUS      DEPLOYED AT           DEPLOYED BY
v1.4.3      ✓ current   healthy     2026-03-28 14:22 UTC  alice@company.com
v1.4.2                  available   2026-03-20 09:11 UTC
v1.4.1                  available   2026-03-15 17:44 UTC
```

**Output (JSON, for agents):**
```json
{
  "current": "v1.4.3",
  "versions": [
    { "version": "v1.4.3", "status": "deployed", "deployedAt": "...", "deployedBy": "..." },
    { "version": "v1.4.2", "status": "available" }
  ]
}
```

---

## Diff Summary Design

The diff is generated before any deployment. It compares the currently deployed version with the target version.

### Sources of Diff Data

The diff engine pulls from whatever metadata the deployment backend exposes. Design the `DiffProvider` interface to be pluggable:

```go
type DiffProvider interface {
    GetDiff(ctx context.Context, env, service, from, to string) (*Diff, error)
}

type Diff struct {
    FromVersion  string
    ToVersion    string
    Direction    string   // "upgrade" | "downgrade" | "redeploy"
    Commits      []Commit
    ChangedFiles []string
    BreakingHints []string
    RawChangelog string
}
```

### Human-Readable Diff Output

```
┌─────────────────────────────────────────────────────────┐
│  Deployment Diff — staging / app                        │
│  v1.4.1  →  v1.4.3                                      │
├─────────────────────────────────────────────────────────┤
│  Direction:   UPGRADE (+2 versions)                     │
│  Commits:     7                                         │
│  ⚠  Breaking hints: none                               │
├─────────────────────────────────────────────────────────┤
│  Commits included:                                      │
│  abc1234  feat: add retry logic to payment service      │
│  def5678  fix: correct timeout on webhook handler       │
│  ghi9012  chore: bump dependencies                      │
│  ...                                                    │
└─────────────────────────────────────────────────────────┘
```

### JSON Diff Output (Agent / Skill Mode)

```json
{
  "diff": {
    "from": "v1.4.1",
    "to": "v1.4.3",
    "direction": "upgrade",
    "commitCount": 7,
    "breakingHints": [],
    "commits": [...]
  },
  "approved": false
}
```

---

## Approval Gate

```go
type ApprovalMode int

const (
    ApprovalInteractive ApprovalMode = iota  // prompt user
    ApprovalCI                               // auto-approve, no prompt
    ApprovalAgent                            // auto-approve, JSON output
)

func RequestApproval(diff *Diff, mode ApprovalMode) (bool, error)
```

### Mode Detection (Priority Order)

1. `--ci` flag or `CI=true` env var → CI mode
2. `--agent` flag or `DEPLOYCLI_AGENT=true` env var → Agent mode
3. `DEPLOYCLI_TOKEN` set and no TTY detected → Agent mode
4. TTY present → Interactive mode

### Interactive Prompt

```
Deploy app v1.4.1 → v1.4.3 to staging?
Includes 7 commits. No breaking changes detected.

  [y] Yes, deploy now
  [n] No, abort
  [d] Show full diff

Choice [y/n/d]: _
```

### CI / Agent Mode

Approval is granted automatically. The diff is still computed and included in the JSON output so that the calling agent has full context.

---

## Skill / Agent Integration

### MCP Skill Wrapper (`pkg/skill/skill.go`)

Exposes the CLI as an MCP-compatible tool with a JSON-in / JSON-out contract:

```go
// Input schema (as JSON)
type SkillInput struct {
    Command  string            `json:"command"`   // "deploy_app" | "deploy_api" | "list_versions"
    Env      string            `json:"env"`
    Version  string            `json:"version,omitempty"`
    Service  string            `json:"service,omitempty"`
    Token    string            `json:"token"`
    AutoApprove bool           `json:"auto_approve"`
}

// Output schema
type SkillOutput struct {
    Success    bool        `json:"success"`
    Message    string      `json:"message"`
    Diff       *Diff       `json:"diff,omitempty"`
    Versions   []Version   `json:"versions,omitempty"`
    Error      string      `json:"error,omitempty"`
}
```

The skill wrapper calls the same internal functions as the CLI commands — no duplication.

### Agent-Friendly CLI Contract

When `--output json` or `--agent` is set:

- All output goes to stdout as a single JSON object
- Logs and progress go to stderr (or are suppressed)
- Exit codes are meaningful: `0` success, `1` user abort, `2` auth error, `3` deployment failed, `4` version not found

---

## Configuration File

Location: `~/.deploycli/config.yaml`

```yaml
api_base_url: https://deploy.yourcompany.com
gitlab_url: https://gitlab.yourcompany.com
gitlab_client_id: <oauth-app-client-id>
gitlab_redirect_port: 9876        # local callback port

default_env: staging

# Optional: per-environment overrides
environments:
  production:
    require_approval: true
    notify_slack: true
```

---

## Key Dependencies

| Package | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI command framework |
| `github.com/spf13/viper` | Config file + env var management |
| `github.com/zalando/go-keyring` | OS keychain token storage |
| `golang.org/x/oauth2` | OAuth2 token exchange |
| `github.com/charmbracelet/lipgloss` | Terminal diff/table rendering |
| `github.com/charmbracelet/bubbletea` | Interactive approval prompt (TUI) |
| `github.com/fatih/color` | Fallback color output |
| `encoding/json` | JSON output (stdlib) |
| `net/http` | HTTP client for deploy API (stdlib) |

---

## Implementation Phases

### Phase 1 — Core Scaffold

- `main.go` + Cobra root command
- Config loading with Viper
- Auth interface + token mode implementation
- Deployment API client (stubbed)
- `versions list` command (text + JSON output)

### Phase 2 — GitLab OAuth

- PKCE flow implementation
- Local callback HTTP server
- Keychain storage + token refresh
- `deploycli auth login` / `deploycli auth logout` commands
- Auto-detection of human vs agent mode

### Phase 3 — Deploy Commands

- `deploy app` and `deploy api` commands
- Diff provider interface + implementation
- Diff renderer (text + JSON)
- Deployment status polling and progress output

### Phase 4 — Approval Gate

- Interactive TUI prompt (bubbletea)
- CI / agent auto-approve bypass
- Approval embedded in JSON output for agents

### Phase 5 — Skill Package

- `pkg/skill` JSON wrapper
- Input/output schema with validation
- End-to-end test: agent calls skill → deploys → returns diff + result

### Phase 6 — Hardening

- Structured logging to stderr
- Retry logic on HTTP calls
- Token expiry detection + re-auth prompt
- `--dry-run` flag (compute diff, never deploy)
- Docs: `deploycli help`, man pages, README

---

## Security Considerations

- PKCE (not implicit flow) — no client secret required in the binary
- Tokens stored in OS keychain, never in plaintext config files
- Agent tokens passed via env var (`DEPLOYCLI_TOKEN`), not CLI flags (avoids `ps` leakage)
- All API calls use TLS; the deploy API base URL is validated at startup
- Production deployments can be gated server-side on token scopes

---

## Testing Strategy

| Layer | Approach |
|---|---|
| Unit | Table-driven tests for diff engine, approval gate, output formatters |
| Auth | Mock OAuth server; test PKCE exchange and keychain read/write |
| Integration | Docker Compose with a stub deploy API + GitLab mock |
| Skill contract | Golden-file tests: given input JSON → assert output JSON shape |
| CI simulation | Run full deploy flow with `CI=true` in GitHub Actions against stub API |
