#!/bin/sh
# Minimal hole for cmux-notify-bridge: allow only TCP 8765 to the Docker Desktop host gateway.
# Using ROUTER_ALLOW_HOSTS would allow traffic per-IP (all ports), which is too broad,
# so we insert a port-specific iptables rule at the top of the FORWARD chain.
# The existing RFC1918 / link-local DROP rules in entrypoint.sh remain effective.
set -eu

host_ip="$(getent ahostsv4 host.docker.internal | awk 'NR==1{print $1}')"
if [ -z "${host_ip}" ]; then
  echo "router-allow-bridge.sh: cannot resolve host.docker.internal" >&2
  exit 1
fi

iptables -I FORWARD 1 \
  -d "${host_ip}" \
  -p tcp --dport 8765 \
  -m conntrack --ctstate NEW \
  -j ACCEPT

echo "router-allow-bridge.sh: allowed TCP ${host_ip}:8765 (cmux-notify-bridge)"