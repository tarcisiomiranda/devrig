# devrig agent skills

Canonical skill definition: [`devrig/SKILL.md`](devrig/SKILL.md)

Follows the [Agent Skills](https://agentskills.io/specification) open format
(`name` + `description` frontmatter + markdown body).

## Install with the binary installer (optional)

The curl/`install.sh` installer **does not** install agent skills by default.

```bash
# Binary only (default)
curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/devrig/main/install.sh | bash

# Binary + detect AIs and install SKILL.md globally
curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/devrig/main/install.sh \
  | DEVRIG_INSTALL_SKILLS=1 bash
```

Truthy values: `1`, `true`, `yes`, `on`.

## Install from a git checkout (detects AIs on this PC)

From the repository root:

```bash
# See which agents are present
mise run skills:list
# or: python scripts/install_skills.py --list

# Install for detected agents (project + global paths)
mise run skills:install
# or: python scripts/install_skills.py

# Force every known tool path
mise run skills:install:all

# Only list / dry-run / scope
python scripts/install_skills.py --dry-run
python scripts/install_skills.py --project-only
python scripts/install_skills.py --global-only
python scripts/install_skills.py --only claude --only grok
```

The installer (`scripts/install_skills.py`) looks for config dirs under `$HOME`
(e.g. `~/.claude`, `~/.codex`, `~/.grok`) and binaries on `PATH`, then copies
`skills/devrig/SKILL.md` only where those tools live.

`./scripts/install-skills.sh` is a thin wrapper around the same Python script.

## Discovery paths by tool

| Tool | Project | Global |
|------|---------|--------|
| **Claude Code** | `.claude/skills/devrig/SKILL.md` | `~/.claude/skills/devrig/SKILL.md` |
| **OpenAI Codex** | `.codex/skills/devrig/SKILL.md` | `~/.codex/skills/devrig/SKILL.md` |
| **OpenCode** | `.opencode/skills/devrig/SKILL.md` | `~/.config/opencode/skills/devrig/SKILL.md` |
| **Cursor** | `.cursor/skills/devrig/SKILL.md` | `~/.cursor/skills/devrig/SKILL.md` |
| **Grok** | `.grok/skills/devrig/SKILL.md` | `~/.grok/skills/devrig/SKILL.md` |
| **Generic / multi-agent** | `.agents/skills/devrig/SKILL.md` | `~/.agents/skills/devrig/SKILL.md` |
| **Gemini CLI** | `.agents/skills/devrig/SKILL.md` | `~/.gemini/skills/devrig/SKILL.md` |
| **Windsurf** | `.agents/skills/devrig/SKILL.md` | `~/.codeium/windsurf/skills/devrig/SKILL.md` |
| **Continue** | `.continue/skills/devrig/SKILL.md` | `~/.continue/skills/devrig/SKILL.md` |

When editing the skill body, change `skills/devrig/SKILL.md` only, then re-run
the installer so tool-specific copies stay in sync.

## When agents should load this skill

- Tests or a local loop need a real database
- Setting `TEST_DATABASE_URL`
- Replacing ad-hoc `docker run postgres`
- Cleaning up throwaway containers (`devrig down` / `devrig list`)
