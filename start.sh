#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export TG_APP_ID="${TG_APP_ID:-2040}"
export TG_APP_HASH="${TG_APP_HASH:-b18441a1ff607e10a989891a5462e627}"
WORKERS="${WORKERS:-100}"

print_banner() {
  cat <<'EOF'
============================================================
 tg-session-checker-go
 Telegram Session 批量筛号工具
============================================================
 1. 只筛存活
 2. 只跑 SpamBot
 3. 存活 + SpamBot
============================================================
EOF
}

prompt_mode() {
  while true; do
    read -r -p "请选择模式 [1-3] (默认 1): " choice
    choice="${choice:-1}"
    case "$choice" in
      1)
        MODE="alive"
        MODE_LABEL="只筛存活"
        return
        ;;
      2)
        MODE="spam"
        MODE_LABEL="只跑 SpamBot"
        return
        ;;
      3)
        MODE="both"
        MODE_LABEL="存活 + SpamBot"
        return
        ;;
      *)
        echo "输入不对，填 1 / 2 / 3。"
        ;;
    esac
  done
}

prompt_input() {
  while true; do
    read -r -p "请输入 session 目录 / 单个 .session / .zip 路径: " INPUT_PATH
    INPUT_PATH="${INPUT_PATH:-}"
    if [[ -n "$INPUT_PATH" && -e "$INPUT_PATH" ]]; then
      return
    fi
    echo "路径不存在，重新输入。"
  done
}

prompt_output() {
  read -r -p "输出目录 (默认 $SCRIPT_DIR/output): " OUTPUT_DIR
  OUTPUT_DIR="${OUTPUT_DIR:-$SCRIPT_DIR/output}"
}

run_cli() {
  mkdir -p "$OUTPUT_DIR"
  cd "$SCRIPT_DIR"

  echo
  echo "开始执行："
  echo "  模式: $MODE_LABEL ($MODE)"
  echo "  输入: $INPUT_PATH"
  echo "  输出: $OUTPUT_DIR"
  echo "  Workers: $WORKERS"
  echo "  AppID: $TG_APP_ID"
  echo

  exec go run . -input "$INPUT_PATH" -mode "$MODE" -workers "$WORKERS" -out "$OUTPUT_DIR"
}

if [[ $# -gt 0 ]]; then
  INPUT_PATH="$1"
  MODE="${MODE:-alive}"
  OUTPUT_DIR="${OUTPUT_DIR:-$SCRIPT_DIR/output}"
  case "$MODE" in
    alive) MODE_LABEL="只筛存活" ;;
    spam) MODE_LABEL="只跑 SpamBot" ;;
    both) MODE_LABEL="存活 + SpamBot" ;;
    *)
      echo "MODE 只支持 alive / spam / both"
      exit 1
      ;;
  esac
  run_cli
fi

print_banner
prompt_mode
prompt_input
prompt_output
run_cli
