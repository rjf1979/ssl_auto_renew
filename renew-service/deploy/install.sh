#!/usr/bin/env bash
set -euo pipefail

PREFIX="${PREFIX:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc/ssl-auto-renew}"
DATA_DIR="${DATA_DIR:-/var/lib/ssl-auto-renew}"
CERT_DIR="${CERT_DIR:-/etc/ssl-auto-renew/certs}"
BIN="${1:-./sslctl}"

if [[ "${EUID}" -ne 0 ]]; then
  echo "please run as root" >&2
  exit 1
fi
if [[ ! -x "${BIN}" ]]; then
  echo "binary not found or not executable: ${BIN}" >&2
  exit 1
fi

install -d -m 0750 "${CONFIG_DIR}/secrets" "${CERT_DIR}" "${DATA_DIR}"
install -d -m 0755 "${PREFIX}"
install -m 0755 "${BIN}" "${PREFIX}/sslctl"

if ! id -u ssl-renew >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/ssl-auto-renew --shell /usr/sbin/nologin ssl-renew
fi

chown -R ssl-renew:ssl-renew "${DATA_DIR}" "${CERT_DIR}"
chown root:ssl-renew "${CONFIG_DIR}" "${CONFIG_DIR}/secrets"
chmod 0750 "${CONFIG_DIR}"
chmod 0700 "${CONFIG_DIR}/secrets"

install -m 0644 deploy/systemd/ssl-auto-renew.service /etc/systemd/system/ssl-auto-renew.service
install -m 0644 deploy/systemd/ssl-auto-renew.timer /etc/systemd/system/ssl-auto-renew.timer
systemctl daemon-reload
systemctl enable --now ssl-auto-renew.timer

echo "installed sslctl and enabled ssl-auto-renew.timer"
echo "next: install ${CONFIG_DIR}/config.yaml and secrets, then run sslctl config validate"
