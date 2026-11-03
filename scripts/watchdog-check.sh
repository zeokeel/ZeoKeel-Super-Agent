#!/bin/sh
# watchdog-check.sh — the 006:133-141 hourly check, as a script.
#
# STATUS (2026-11-03, after Wave 2): `zeokeel status` exists, so this script is
# usable. Wire it into cron / a systemd timer / launchd StartCalendarInterval as
# described in design/ops-runbook.md §7. It still defensively detects an older
# build without the subcommand and says so instead of pretending to supervise.
#
# Contract (identical to 006:133-141):
#   0 → do nothing, say nothing
#   1 → the service is stopped/duplicated/unreachable/runtime error: restart may
#       fix it; run `zeokeel status` once more after a restart, then report
#   2 → component error (Source/sync/...): do NOT restart; report `errors`
#   3 → waiting for setup: do NOT restart; report `setup`
#   `manually_stopped: true` → leave it stopped, never restart
#
# Usage:
#   scripts/watchdog-check.sh [--checkout DIR] [--restart-cmd CMD]
#   ZEOKEEL_BIN=./zeokeel scripts/watchdog-check.sh
set -u

checkout=$(cd "$(dirname "$0")/.." && pwd)
bin="${ZEOKEEL_BIN:-$checkout/zeokeel}"
restart_cmd="${ZEOKEEL_RESTART_CMD:-}"

usage() {
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
	case "$1" in
	--checkout)
		[ $# -ge 2 ] || { echo "watchdog-check: --checkout needs a value" >&2; exit 2; }
		checkout=$2
		shift 2
		;;
	--restart-cmd)
		[ $# -ge 2 ] || { echo "watchdog-check: --restart-cmd needs a value" >&2; exit 2; }
		restart_cmd=$2
		shift 2
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		echo "watchdog-check: unknown argument: $1" >&2
		exit 2
		;;
	esac
done

if [ ! -x "$bin" ] && ! command -v zeokeel >/dev/null 2>&1; then
	# No binary at all: not a supervised deployment. Say it and stop.
	echo "watchdog-check: no zeokeel binary found (looked at $bin)"
	exit 0
fi
[ -x "$bin" ] || bin=zeokeel

# Probe the status subcommand without pretending an error is a health signal.
probe=$("$bin" status --json 2>&1)
probe_rc=$?
case "$probe_rc" in
0|1|2|3)
	# A real status implementation. Fall through to the 006 semantics below.
	;;
*)
	case "$probe" in
	*"flag provided but not defined"*|*"unknown command"*|*"not implemented"*)
		echo "watchdog-check: this build has no 'zeokeel status' subcommand" \
			"(older build? see design/ops-runbook.md); nothing to supervise."
		exit 0
		;;
	esac
	echo "watchdog-check: status probe failed (exit $probe_rc): $probe" >&2
	exit 1
	;;
esac

json_get() { # crude extraction, enough for the documented flat probes
	printf '%s' "$1" | tr -d '\n' | sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\([^,}]*\).*/\1/p"
}

manually_stopped=$(json_get "$probe" manually_stopped)
if [ "$manually_stopped" = "true" ]; then
	echo "watchdog-check: manually stopped; leaving it stopped"
	exit 0
fi

case "$probe_rc" in
0)
	# Healthy: silence is the feature.
	exit 0
	;;
1)
	echo "watchdog-check: status=1 (restart may fix it)"
	if [ -n "$restart_cmd" ]; then
		sh -c "$restart_cmd"
		after=$("$bin" status --json 2>&1)
		after_rc=$?
		if [ "$after_rc" -eq 0 ]; then
			echo "watchdog-check: recovered after restart"
			exit 0
		fi
		echo "watchdog-check: still not healthy after restart (exit $after_rc): $after" >&2
		exit "$after_rc"
	fi
	echo "watchdog-check: no --restart-cmd given; reporting instead of restarting" >&2
	exit 1
	;;
2)
	echo "watchdog-check: status=2 (component error; a restart will not fix it)" >&2
	echo "$probe" >&2
	exit 2
	;;
3)
	echo "watchdog-check: status=3 (waiting for setup; a restart will not fix it)" >&2
	echo "$probe" >&2
	exit 3
	;;
esac
