#!/usr/bin/env bash
# ws_netlab.sh measures server-to-browser runtime frames on shaped links.
# It runs in a Linux container with NET_ADMIN. A netem qdisc on loopback shapes the
# link, so the kernel TCP stack applies, including slow start after idle.
# tooling/netlab sets the qdisc over netlink, so no package install is needed.
# Usage (from the repo root): make bench-ws-netlab
set -euo pipefail

out="${NETLAB_OUT:-/work/benchmark-results/ws_netlab/results.jsonl}"
mkdir -p "$(dirname "$out")"
: > "$out"

echo "kernel: $(uname -r)"
echo "tcp_slow_start_after_idle: $(cat /proc/sys/net/ipv4/tcp_slow_start_after_idle)"
echo "tcp_congestion_control: $(cat /proc/sys/net/ipv4/tcp_congestion_control)"

echo "compiling tools"
(cd /work/tooling/netlab && timeout 300 go build -o /tmp/netlab .)
(cd /work/server-kit/go && timeout 600 go test -tags netlab -c -o /tmp/netlab.test ./httpserver/)
echo "tools compiled"

# name|one-way delay ms|rate kbit/s|loss percent|idle ms
# netem on loopback applies in both directions, so RTT is twice the delay.
profiles=(
  "fiber-ng|10|100000|0|1000"
  "4g-good|30|20000|0|1000"
  "4g-typical-ng|50|8000|0|1500"
  "3g-congested|100|1500|1|2000"
  "edge-2g|200|256|1|3000"
)

cleanup() { /tmp/netlab clear; }
trap cleanup EXIT

for entry in "${profiles[@]}"; do
  IFS='|' read -r name delay rate loss idle <<<"$entry"
  cleanup
  /tmp/netlab set -delay "${delay}ms" -rate-kbit "$rate" -loss "$loss"
  echo "profile $name: rtt=$((delay * 2))ms rate=${rate}kbit loss=${loss}%"
  NETLAB_PROFILE="$name" NETLAB_IDLE_MS="$idle" NETLAB_TRIALS="${NETLAB_TRIALS:-9}" \
    timeout 1800 /tmp/netlab.test -test.run '^TestWSNetlab$' -test.count=1 -test.v -test.timeout 30m 2>/dev/null \
    | grep '^{"profile"' >>"$out"
done
echo "results: $out ($(wc -l <"$out") rows)"
