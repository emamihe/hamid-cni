#!/bin/sh
# Install hamid-cni + required standard CNI plugins onto the host.
set -eu

BIN_DIR="${CNI_BIN_DIR:-/opt/cni/bin}"
CONF_DIR="${CNI_CONF_DIR:-/etc/cni/net.d}"
CONF_NAME="${CNI_CONF_NAME:-10-hamid-cni.conflist}"
CNI_PLUGINS_VERSION="${CNI_PLUGINS_VERSION:-v1.6.2}"

mkdir -p "${BIN_DIR}" "${CONF_DIR}"

echo "installing hamid-cni into ${BIN_DIR}"
cp -f /usr/local/bin/hamid-cni "${BIN_DIR}/hamid-cni"
chmod 755 "${BIN_DIR}/hamid-cni"

need_plugins=0
for bin in loopback portmap bandwidth host-local; do
  if [ ! -x "${BIN_DIR}/${bin}" ]; then
    need_plugins=1
    break
  fi
done

install_from_image() {
  for bin in loopback portmap bandwidth host-local; do
    if [ -f "/opt/cni/bin/${bin}" ]; then
      cp -f "/opt/cni/bin/${bin}" "${BIN_DIR}/${bin}"
      chmod 755 "${BIN_DIR}/${bin}"
    fi
  done
}

install_from_image

# Fallback: download official plugins if loopback is still missing (old image / empty host).
if [ ! -x "${BIN_DIR}/loopback" ]; then
  arch="$(uname -m)"
  case "${arch}" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    armv7l) arch=arm ;;
    *) echo "unsupported arch: ${arch}"; exit 1 ;;
  esac

  echo "loopback missing; downloading containernetworking plugins ${CNI_PLUGINS_VERSION} (${arch})"
  tmp="$(mktemp)"
  url="https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS_VERSION}/cni-plugins-linux-${arch}-${CNI_PLUGINS_VERSION}.tgz"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "${tmp}" "${url}"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "${tmp}" "${url}"
  else
    echo "curl/wget not available and loopback not packaged in image" >&2
    exit 1
  fi
  tar -xzf "${tmp}" -C "${BIN_DIR}"
  rm -f "${tmp}"
fi

if [ ! -x "${BIN_DIR}/loopback" ]; then
  echo "ERROR: ${BIN_DIR}/loopback is still missing" >&2
  ls -la "${BIN_DIR}" >&2 || true
  exit 1
fi

if [ -f "/cni/${CONF_NAME}" ]; then
  echo "installing CNI conflist ${CONF_NAME} into ${CONF_DIR}"
  cp -f "/cni/${CONF_NAME}" "${CONF_DIR}/${CONF_NAME}"
fi

echo "CNI install complete:"
ls -la "${BIN_DIR}/hamid-cni" "${BIN_DIR}/loopback" "${BIN_DIR}/portmap" 2>/dev/null || ls -la "${BIN_DIR}"
