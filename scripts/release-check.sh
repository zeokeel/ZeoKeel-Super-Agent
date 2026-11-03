#!/bin/sh
# The release gate. Every check here is mechanical on purpose: a release that
# depends on someone remembering is a release that ships broken. Anything that
# cannot be checked on this host is reported as SKIP, never as a pass.
#
# Consistency contract (Wave 1): the engineering gate is exactly
#   scripts/verify-fixes.sh
# which is also what CI runs and what `make verify` runs. This script adds only
# release-specific checks (version, artifacts, docs, acceptance test), so there
# is no "green locally / red in CI" gap. Platform-only checks (six-way cross
# build, window under Xvfb) stay in CI and are reported here as SKIP.
set -u
cd "$(dirname "$0")/.."

VERSION="$(cat VERSION)"
FAIL=0
PASS=0
SKIP=0

ok()   { PASS=$((PASS+1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  \033[31m✗\033[0m %s\n' "$1"; }
skip() { SKIP=$((SKIP+1)); printf '  \033[33m–\033[0m %s\n' "$1"; }
head_() { printf '\n\033[1m%s\033[0m\n' "$1"; }

head_ "版本"
case "$VERSION" in
  ''|*-dev) bad "VERSION 仍是 $VERSION：连候选版都不是" ;;
  *-rc.*)
    ok "VERSION=$VERSION（发布候选；1.0.0 仍需完成 RELEASE.md 的全部勾选项）"
    printf '    \033[33m注意\033[0m：rc 候选不等于 1.0.0；RELEASE.md 的 F 组（阻塞项）必须清空\n' ;;
  1.0.0) ok "VERSION=1.0.0（正式版）" ;;
  *) bad "VERSION=$VERSION 不是 1.0.0 也不是候选版" ;;
esac

head_ "工程与测试（= scripts/verify-fixes.sh，与 CI 同一个闸门）"
if sh scripts/verify-fixes.sh >/tmp/zrelease-verify.log 2>&1; then
  ok "verify-fixes.sh PASS（build/test/vet/nocgo/race，日志 /tmp/zrelease-verify.log）"
else
  bad "verify-fixes.sh FAIL，见 /tmp/zrelease-verify.log"
fi

head_ "用户路径无 TODO/FIXME"
if [ -z "$(grep -rn 'TODO\|FIXME' --include='*.go' internal/ cmd/ | grep -v _test)" ]; then
  ok "用户路径无 TODO/FIXME"
else
  bad "还有 TODO/FIXME：$(grep -rn 'TODO\|FIXME' --include='*.go' internal/ cmd/ | grep -v _test | wc -l | tr -d ' ') 处"
fi

head_ "产物"
for f in "dist/zeokeel-$VERSION-linux-x86_64.tar.gz" \
         "dist/zeokeel-$VERSION-macos-arm64.zip" \
         "dist/zeokeel-$VERSION-windows-x86_64.zip"; do
  if [ -f "$f" ]; then
    sz=$(stat -c%s "$f" 2>/dev/null || stat -f%z "$f")
    if [ "$sz" -lt 41943040 ]; then ok "$(basename "$f") $(($sz/1024/1024))MB"; else bad "$f 超过 40MB"; fi
  else
    bad "缺少 $f（跑 make package）"
  fi
done

head_ "文档"
for f in README.md CHANGELOG.md RELEASE.md design/zeokeel-go-mygo-design.md \
         design/rejected-concepts.md design/ops-runbook.md design/how-to-add-a-source.md \
         packaging/systemd/zeokeel.service packaging/launchd/com.zeokeel.agent.plist; do
  if [ -s "$f" ]; then ok "$f"; else bad "缺少 $f"; fi
done

head_ "验收测试"
if grep -q 'TestEndToEndFromChatToReply' cmd/zeokeel/e2e_test.go 2>/dev/null; then
  ok "端到端验收测试存在"
else
  bad "缺少端到端验收测试"
fi

head_ "无法在本机验证（CI/真机负责）"
skip "六平台交叉编译（CI cross job）"
skip "Xvfb 下的真实窗口冒烟（CI window job）"
skip "macOS 签名与公证（需要 macOS + 开发者证书）"
skip "macOS .dmg（需要 hdiutil）"
skip "Windows 安装器在真机上装一遍（需要 Windows）"
skip "真实凭据下的 GitHub / 飞书 / 钉钉端到端（IM 已接线：Feishu 长轮询 + DingTalk Stream；CI 无凭据）"

printf '\n\033[1m结果\033[0m: %d 通过, %d 失败, %d 跳过\n' "$PASS" "$FAIL" "$SKIP"
if [ "$FAIL" -gt 0 ]; then
  printf '\033[31m不能发布\033[0m\n'
  exit 1
fi
printf '\033[32m机械检查全部通过\033[0m（剩余 SKIP 项需要相应硬件或凭据；RELEASE.md F 组阻塞项仍需人工确认）\n'
