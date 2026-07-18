#!/bin/bash
set -e

GLOBAL_DIR="/Users/liheng/.agents/skills"
CLINE_DIR="/Users/liheng/.cline/skills"
CLAUDE_DIR="/Users/liheng/.claude/skills"

echo ">>> 全局技能目录: $GLOBAL_DIR"
echo "    技能数量: $(ls "$GLOBAL_DIR" | wc -l)"

# 重建 Cline 技能目录
echo ">>> 重建 $CLINE_DIR ..."
rm -rf "$CLINE_DIR"
mkdir -p "$CLINE_DIR"

for skill_dir in "$GLOBAL_DIR"/*/; do
    skill_name=$(basename "$skill_dir")
    target=$(cd "$skill_dir" && pwd -P 2>/dev/null || echo "$skill_dir")
    ln -sf "$target" "$CLINE_DIR/$skill_name"
done
echo "    Cline 技能数量: $(ls "$CLINE_DIR" | wc -l)"

# 确保 Claude Code 技能目录也完整
echo ">>> 同步 $CLAUDE_DIR ..."
for skill_dir in "$GLOBAL_DIR"/*/; do
    skill_name=$(basename "$skill_dir")
    target=$(cd "$skill_dir" && pwd -P 2>/dev/null || echo "$skill_dir")
    if [ ! -e "$CLAUDE_DIR/$skill_name" ]; then
        ln -sf "$target" "$CLAUDE_DIR/$skill_name"
    fi
done
echo "    Claude Code 技能数量: $(ls "$CLAUDE_DIR" | wc -l)"

echo ""
echo "=== 最终汇总 ==="
echo "全局 (~/.agents/skills/):      $(ls "$GLOBAL_DIR" | wc -l) 个技能"
echo "Claude Code (~/.claude/skills/): $(ls "$CLAUDE_DIR" | wc -l) 个技能"
echo "Cline (~/.cline/skills/):       $(ls "$CLINE_DIR" | wc -l) 个技能"
echo "项目 (shm-go/.agents/):         已删除"
echo ""
echo "全部技能:"
ls "$GLOBAL_DIR" | sort