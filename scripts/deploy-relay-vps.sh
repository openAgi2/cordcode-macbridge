#!/usr/bin/env bash
# 部署 relay-server 到生产 VPS（relay.byteseek.uk）。
# 用法：scripts/deploy-relay-vps.sh
#
# 凭据来自环境变量（~/.zshrc 定义，不入仓库）：
#   CCCODE_RELAY_VPS_HOST, CCCODE_RELAY_VPS_USER, CCCODE_RELAY_VPS_PASS
# ssh 别名 cccode-relay-prod (~/.ssh/config) 已含 host/user；密码经 sshpass -e (SSHPASS) 非交互传入。
#
# 首次部署（建用户/目录/systemd/nginx/TLS）见 docs/../relay-server-install.md
# （或 /Users/jacklee/Projects/opencode-cc-connect/relay-server-install.md）。
# 本脚本只做"更新二进制"（该文档 §13），含备份+SHA校验+回滚。
set -euo pipefail

SSH_HOST=cccode-relay-prod
REMOTE_BIN=/opt/cccode-relay/bin/relay-server
LOCAL_BIN="${1:-/tmp/cccode-relay-server}"

# 加载凭据（新 shell 可能未 source ~/.zshrc）
# shellcheck disable=SC1091
[ -n "${CCCODE_RELAY_VPS_PASS:-}" ] || { [ -f ~/.zshrc ] && source ~/.zshrc 2>/dev/null || true; }
: "${CCCODE_RELAY_VPS_PASS:?CCCODE_RELAY_VPS_PASS 未设置（检查 ~/.zshrc）}"
export SSHPASS="$CCCODE_RELAY_VPS_PASS"

[ -x "$LOCAL_BIN" ] || { echo "❌ 本地二进制不存在或不可执行: $LOCAL_BIN（先交叉编译，见下方注释）"; exit 1; }
EXPECTED_SHA=$(shasum -a 256 "$LOCAL_BIN" | awk '{print $1}')
echo "本地二进制: $LOCAL_BIN"
echo "本地 sha256: $EXPECTED_SHA"
echo "目标: $SSH_HOST ($CCCODE_RELAY_VPS_USER@${CCCODE_RELAY_VPS_HOST:-<via alias>}) → $REMOTE_BIN"
echo

# 该 VPS 的 sshd banner exchange 偶发超时（UseDNS 慢/间歇网络），所有 ssh/scp 调用都重试。
ssh_retry() {
  local tries=0
  until sshpass -e ssh -o StrictHostKeyChecking=accept-new \
        -o ConnectTimeout=30 -o ServerAliveInterval=10 -o ConnectionAttempts=2 \
        "$SSH_HOST" "$@" 2>&1 | grep -vE "Warning: Permanently added"; do
    tries=$((tries+1)); [ $tries -ge 5 ] && { echo "❌ ssh 重试 5 次仍失败"; return 1; }
    echo "   (ssh 第 $tries 次失败，banner 超时常见，${tries}5s 后重试...)"; sleep $((tries*5))
  done
}
scp_retry() {  # $1=本地 $2=远程
  local tries=0
  until sshpass -e scp -o StrictHostKeyChecking=accept-new \
        -o ConnectTimeout=30 -o ServerAliveInterval=10 -o ConnectionAttempts=2 \
        "$1" "$SSH_HOST:$2" 2>&1 | grep -vE "Warning: Permanently added"; do
    tries=$((tries+1)); [ $tries -ge 5 ] && { echo "❌ scp 重试 5 次仍失败"; return 1; }
    echo "   (scp 第 $tries 次失败，重试...)"; sleep $((tries*5))
  done
}

echo "==> 1/6 只读核查现状（架构/路径/服务）"
ssh_retry '
  test "$(uname -m)" = "x86_64" || { echo "❌ 架构非 x86_64（本二进制是 amd64）"; exit 1; }
  [ -f '"$REMOTE_BIN"' ] || { echo "❌ '"$REMOTE_BIN"' 不存在——VPS 未按 relay-server-install.md 部署"; exit 1; }
  echo "arch: $(uname -m)"
  echo "当前二进制: $(ls -la '"$REMOTE_BIN"' | awk "{print \$1, \$3\":\"\"\$4, \$5}")"
  echo "当前 sha256: $(sha256sum '"$REMOTE_BIN"' | awk "{print \$1}")"
  systemctl status cccode-relay --no-pager 2>&1 | head -4
' || exit 1

TS=$(ssh_retry 'date -u +%Y%m%dT%H%M%SZ')
echo
echo "==> 2/6 备份旧二进制 → $REMOTE_BIN.bak.$TS"
ssh_retry "cp -p $REMOTE_BIN $REMOTE_BIN.bak.$TS && ls -la $REMOTE_BIN.bak.$TS" || exit 1

echo
echo "==> 3/6 上传新二进制 → $REMOTE_BIN.new"
scp_retry "$LOCAL_BIN" "$REMOTE_BIN.new" || exit 1

echo
echo "==> 4/6 SHA 校验 + 保留旧 owner:group/mode + 原子替换"
ssh_retry "
  set -e
  NEW_SHA=\$(sha256sum $REMOTE_BIN.new | awk '{print \$1}')
  echo \"上传 sha256: \$NEW_SHA\"
  [ \"\$NEW_SHA\" = \"$EXPECTED_SHA\" ] || { echo '❌ SHA 不匹配——保留旧版，删除 .new'; rm -f $REMOTE_BIN.new; exit 1; }
  OLD_OWNER=\$(stat -c '%U:%G' $REMOTE_BIN)
  OLD_MODE=\$(stat -c '%a' $REMOTE_BIN)
  chown \$OLD_OWNER $REMOTE_BIN.new
  chmod \$OLD_MODE $REMOTE_BIN.new
  mv $REMOTE_BIN.new $REMOTE_BIN
  echo \"已替换：owner=\$OLD_OWNER mode=\$OLD_MODE\"
" || exit 1

echo
echo "==> 5/6 重启 cccode-relay"
OLD_PID=$(ssh_retry 'systemctl show -p MainPID --value cccode-relay')
ssh_retry 'systemctl restart cccode-relay' || exit 1
sleep 3

echo
echo "==> 6/6 健康检查"
ssh_retry "
  NEW_PID=\$(systemctl show -p MainPID --value cccode-relay)
  echo \"PID: \$OLD_PID → \$NEW_PID\"
  [ \"\$NEW_PID\" != \"$OLD_PID\" ] || { echo '⚠️  PID 未变，restart 可能未生效，检查 systemctl status'; }
  systemctl is-active cccode-relay
  echo -n 'readyz:  '; curl -s http://127.0.0.1:8780/readyz; echo
  echo -n 'healthz: '; curl -s http://127.0.0.1:8780/healthz; echo
  echo '最近日志:'; journalctl -u cccode-relay --no-pager -n 8 --since '30 seconds ago'
" OLD_PID="$OLD_PID" || exit 1

echo
echo "✅ 部署完成。"
echo "回滚：ssh $SSH_HOST 'mv $REMOTE_BIN.bak.$TS $REMOTE_BIN && systemctl restart cccode-relay'"
echo
echo "交叉编译命令（更新代码后、跑本脚本前）："
echo "  (cd relay-server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o $LOCAL_BIN ./cmd/relay-server)"
