#!/bin/bash
set -e

echo "======================================"
echo " Installing Claude Skills & Plugins"
echo "======================================"
echo ""

# =============================================
# Part 1: Install all Go (Golang) Skills
# Source: samber/cc-skills-golang
# These install into ~/.agents/skills/ via npx skills
# Claude Code picks them up automatically via ~/.claude/skills/ symlinks
# =============================================
echo ">>> [1] Installing all Go skills from samber/cc-skills-golang..."
echo "    This includes: golang-code-style, golang-concurrency, golang-context,"
echo "    golang-design-patterns, golang-error-handling, golang-refactoring,"
echo "    golang-testing, golang-stretchr-testify, golang-benchmark,"
echo "    golang-performance, golang-lint, golang-security,"
echo "    golang-uber-fx, golang-google-wire, golang-samber-do,"
echo "    golang-spf13-cobra, golang-spf13-viper,"
echo "    golang-samber-lo, golang-samber-mo, golang-samber-ro,"
echo "    golang-samber-hot, golang-samber-oops, golang-samber-slog,"
echo "    golang-grpc, golang-documentation"
npx skills add samber/cc-skills-golang --all -y
echo ""

# =============================================
# Part 2: Install missing generic/cross-cutting skills
# These were listed in the catalog but not yet installed
# =============================================
echo ">>> [2] Installing missing generic skills..."

# obsidian-vault (source: mattpocock/skills)
echo "    - obsidian-vault (from mattpocock/skills)"
npx skills add mattpocock/skills@obsidian-vault -y

# review (source: mattpocock/skills)
echo "    - review (from mattpocock/skills)"
npx skills add mattpocock/skills@review -y

# simplify (source: brianlovin/claude-config)
echo "    - simplify (from brianlovin/claude-config)"
npx skills add brianlovin/claude-config@simplify -y

# security-review (source: getsentry/skills)
echo "    - security-review (from getsentry/skills)"
npx skills add getsentry/skills@security-review -y
echo ""

# =============================================
# Part 3: Install Claude Code Official Plugins
# These are the plugins listed in the "Plugins" table
# Source: anthropics/claude-code
# =============================================
echo ">>> [3] Installing Claude Code official plugins..."

# Add the official Claude Code marketplace
echo "    Adding anthropics/claude-code marketplace..."
claude plugin marketplace add anthropics/claude-code 2>/dev/null || echo "    (marketplace may already exist)"

PLUGINS=(
    "agent-sdk-dev"
    "claude-code-setup"
    "claude-md-management"
    "code-modernization"
    "code-review"
    "code-simplifier"
    "commit-commands"
    "explanatory-output-style"
    "feature-dev"
    "frontend-design"
    "github"
    "hookify"
    "learning-output-style"
    "mcp-server-dev"
    "mcp-tunnels"
    "playground"
    "plugin-dev"
    "pr-review-toolkit"
    "project-artifact"
    "ralph-loop"
    "security-guidance"
    "session-report"
    "skill-creator"
)

for plugin in "${PLUGINS[@]}"; do
    echo "    Installing plugin: $plugin..."
    claude plugin install "$plugin" 2>&1 || echo "    Warning: Could not install '$plugin' (may not exist in marketplace)"
done
echo ""

# =============================================
# Part 4: Sync all skills to Cline
# Cline (VS Code extension) reads skills from its own directory
# =============================================
echo ">>> [4] Syncing all skills to Cline..."

mkdir -p ~/.cline/skills

# Sync from ~/.claude/skills to ~/.cline/skills
echo "    Creating symlinks to Cline skills directory..."
count=0
for item in ~/.claude/skills/*; do
    if [ -e "$item" ]; then
        skill_name=$(basename "$item")
        cline_target=~/.cline/skills/"$skill_name"

        # Resolve symlink chain to get actual source
        if [ -L "$item" ]; then
            # It's a symlink - link Cline to the same target
            actual_target=$(readlink -f "$item" 2>/dev/null || readlink "$item")
            if [ ! -e "$cline_target" ]; then
                ln -sf "$actual_target" "$cline_target"
                ((count++))
            fi
        elif [ -d "$item" ]; then
            # It's a directory - link Cline to it
            if [ ! -e "$cline_target" ]; then
                ln -sf "$item" "$cline_target"
                ((count++))
            fi
        fi
    fi
done
echo "    Synced $count skills to Cline."
echo ""

# =============================================
# Summary
# =============================================
echo "======================================"
echo " Installation Complete!"
echo "======================================"
echo ""
echo "Claude Code skills:  $(ls ~/.claude/skills/ 2>/dev/null | wc -l | tr -d ' ')"
echo "Cline skills:        $(ls ~/.cline/skills/ 2>/dev/null | wc -l | tr -d ' ')"
echo "Claude Code plugins: $(claude plugins list 2>/dev/null | grep -c '^' || echo '0')"
echo ""
echo "Installed Go skills:"
ls ~/.claude/skills/ 2>/dev/null | grep golang- | sort
echo ""
echo "To verify: npx skills list"
echo "To verify plugins: claude plugins list"