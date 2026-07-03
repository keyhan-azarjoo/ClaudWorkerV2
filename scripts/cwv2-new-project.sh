#!/bin/bash
# cwv2-new-project.sh — scaffold a NEW, fully-isolated ClaudWorker project (e.g. Chida) alongside the
# default one. Each project is its OWN cwv2 instance: its own config, engine home (assignments,
# worktrees, task-logs, knowledge, leases), secrets/credentials, port and launchd service — nothing is
# shared with any other project.
#
# Usage:   scripts/cwv2-new-project.sh <name> <port>
# Example: scripts/cwv2-new-project.sh chida 8788
#
# After it runs: fill in <dir>/secrets/live.env + <dir>/cwv2.yaml, load the service, expose it at its
# own URL, then add it in the console via the sidebar "＋ Add project…".
set -euo pipefail

NAME="${1:?usage: cwv2-new-project.sh <name> <port>}"
PORT="${2:?usage: cwv2-new-project.sh <name> <port>}"
SLUG="$(echo "$NAME" | tr '[:upper:] ' '[:lower:]-')"
DIR="$HOME/.cw-$SLUG"
HOMED="$DIR/home"

if [ -e "$DIR" ]; then echo "✋ $DIR already exists — pick another name or remove it first." >&2; exit 1; fi

echo "→ Creating isolated project '$NAME' at $DIR (port $PORT)"
mkdir -p "$DIR/secrets" "$HOMED" "$DIR/web"

# Its own web console (copied so the project is self-contained).
cp -R "$HOME/.cw-live/web/ops-console" "$DIR/web/ops-console"

# --- config: OWN repo / Jira / dev branch / accounts (edit before first run) ---
cat > "$DIR/cwv2.yaml" <<YAML
project: $SLUG
engine_home: $HOMED
github:
  commit_identity: { name: "$(git config user.name 2>/dev/null || echo ClaudWorker)", email: "$(git config user.email 2>/dev/null || echo claudworker@localhost)" }   # <-- EDIT if needed
repos:
  - name: backend
    dev_branch: development
    plugin: generic
    url: https://github.com/YOUR-ORG/YOUR-$SLUG-REPO   # <-- EDIT: this project's repo
# Accounts for THIS project only (own CLAUDE_CONFIG_DIR / CODEX_HOME dirs = separate logins).
accounts:
  - { name: $NAME, config_dir: $HOME/.cw-accounts/$SLUG }   # <-- EDIT / create this login dir
jira:
  base_url: https://YOUR-$SLUG.atlassian.net               # <-- EDIT: this project's Jira
  work_jql: 'project = $(echo "$SLUG" | tr '[:lower:]' '[:upper:]') AND status = "To Do" AND labels = ready ORDER BY priority DESC'
usage_guard: { pause_pct: 95, resume_pct: 80, fail_open: false }
dashboard:
YAML

# --- secrets: OWN credentials (never shared). Fill these in. ---
cat > "$DIR/secrets/live.env" <<'ENV'
# Credentials for THIS project only. Different from every other project.
export JIRA_EMAIL=''
export JIRA_API_TOKEN=''
export GITHUB_TOKEN=''
# Optional Sentry (own org/token):
export SENTRY_API_BASE='https://sentry.io/api/0'
export SENTRY_ORG=''
export SENTRY_TOKEN=''
ENV
chmod 600 "$DIR/secrets/live.env"

# --- runner ---
cat > "$DIR/run.sh" <<RUN
#!/bin/bash
export HOME=$HOME
export BROWSER="\$HOME/bin/no-browser"
export CLAUDE_DISABLE_BROWSER_LOGIN=1 CODEX_DISABLE_BROWSER_LOGIN=1 BROWSER_OPEN=none
# Shared registry (names + ports only) so this project's switcher lists all projects. No data shared.
export CWV2_PROJECTS_FILE="\$HOME/.cw-live/projects.json"
set -a; source "$DIR/secrets/live.env"; set +a
export PATH="\$HOME/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/usr/local/share/dotnet:\$HOME/.dotnet/tools"
exec "\$HOME/bin/cwv2" serve --config "$DIR/cwv2.yaml" --mode live \\
  --bind 127.0.0.1:$PORT --web "$DIR/web/ops-console"
RUN
chmod +x "$DIR/run.sh"

# Register this project in the SHARED registry so the main instance proxies /p/$SLUG/ to it and the
# switcher lists it. Only name/slug/port (config) — never any project data or secrets.
REG="$HOME/.cw-live/projects.json"
python3 - "$REG" "$NAME" "$SLUG" "$PORT" <<'PY'
import json, os, sys
reg, name, slug, port = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
try:
    data = json.load(open(reg))
    if not isinstance(data, list): data = []
except Exception:
    data = []
data = [e for e in data if e.get("slug") != slug]
data.append({"name": name, "slug": slug, "port": port})
os.makedirs(os.path.dirname(reg), exist_ok=True)
json.dump(data, open(reg, "w"), indent=2)
print(f"   registered {slug} → port {port} in {reg}")
PY

# --- launchd service ---
PLIST="$HOME/Library/LaunchAgents/com.claudworker.$SLUG.plist"
cat > "$PLIST" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.claudworker.$SLUG</string>
  <key>ProgramArguments</key><array>
    <string>/usr/bin/caffeinate</string><string>-is</string>
    <string>/bin/bash</string><string>$DIR/run.sh</string>
  </array>
  <key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$HOME/Library/Logs/cwv2-$SLUG.out</string>
  <key>StandardErrorPath</key><string>$HOME/Library/Logs/cwv2-$SLUG.log</string>
</dict></plist>
PL

cat <<DONE

✅ Scaffolded project '$NAME' — fully separate from every other project.

   dir:     $DIR
   config:  $DIR/cwv2.yaml         (edit repo/Jira/accounts)
   secrets: $DIR/secrets/live.env  (fill in — chmod 600)
   port:    127.0.0.1:$PORT
   service: com.claudworker.$SLUG

Next:
  1) Edit $DIR/cwv2.yaml and $DIR/secrets/live.env with THIS project's repo, Jira, accounts, tokens.
  2) Create its account login dir(s): CLAUDE_CONFIG_DIR=$HOME/.cw-accounts/$SLUG claude   (log in)
  3) Load it:   launchctl load $PLIST
  4) Restart the MAIN instance so it proxies /p/$SLUG/ + lists it in the switcher (use your main
     service label, e.g.):
       launchctl kickstart -k gui/\$(id -u)/<your-main-cwv2-service-label>

Then it's reachable on the SAME url as the main console, at  /p/$SLUG/  (its own console + token).
Everything (tasks, data, worktrees, credentials, Jira, accounts) stays isolated under $DIR — nothing shared.
DONE
