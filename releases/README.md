# Release notes (YAML)

Each GitHub release can ship **hand-written notes** instead of (or before)
auto-generated commit lists.

## File layout

```
releases/
  v0.1.0.yaml    # notes for tag v0.1.0
  README.md
```

The publisher (`.github/scripts/release.py`) looks for:

```text
releases/<tag>.yaml
releases/<tag>.yml
```

If the file exists, it becomes the GitHub release body. If it is missing, the
script falls back to conventional-commit subjects since the previous SemVer tag.

## Schema

```yaml
# Optional one-paragraph intro under "## What's new"
highlights: |
  Short summary of the release for humans.

# Bullet lists (any can be omitted when empty)
features:
  - "User-facing feature in plain language"
  - "Another feature — markdown **bold** is fine"

fixes:
  - "Bug fix description"

changes:
  - "Docs, UX polish, refactors that are worth calling out"

breaking:
  - "Incompatible change (if any)"

# Optional: previous tag for the compare link (auto-detected when omitted)
# previous: v0.1.0
```

Rules:

- Keys are optional; omit empty sections.
- List items are strings (quote if they contain `:` or start with special YAML chars).
- `highlights` may use a folded (`>`) or literal (`|`) block.
- The `tag` field is optional; when present it must match the release tag.

## Workflow

1. Implement the release.
2. Add `releases/vX.Y.Z.yaml` describing what shipped.
3. Commit, tag `vX.Y.Z`, push the tag (or use the Release workflow_dispatch).
4. CI builds linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and
   publishes the GitHub Release with this body.

Asset names stay `devrig_<os>_<arch>` — the same shape `install.sh` builds
from `uname`. Changing one without the other breaks installation.
