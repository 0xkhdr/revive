#!/bin/sh
set -eu

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) asset=rv_linux_amd64.tar.gz ;;
  Linux/aarch64 | Linux/arm64) asset=rv_linux_arm64.tar.gz ;;
  Darwin/x86_64) asset=rv_darwin_amd64.tar.gz ;;
  Darwin/arm64) asset=rv_darwin_arm64.tar.gz ;;
  *) echo "Unsupported platform: $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac

install_dir=${INSTALL_DIR:-"$HOME/.local/bin"}
download_url=https://github.com/0xkhdr/revive/releases/latest/download
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

curl -fsSLo "$tmp_dir/$asset" "$download_url/$asset"
curl -fsSLo "$tmp_dir/checksums.txt" "$download_url/checksums.txt"
expected=$(awk -v asset="$asset" '$2 == asset { print $1 }' "$tmp_dir/checksums.txt")
[ -n "$expected" ] || { echo "Checksum not found for $asset" >&2; exit 1; }

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp_dir/$asset" | awk '{ print $1 }')
else
  actual=$(shasum -a 256 "$tmp_dir/$asset" | awk '{ print $1 }')
fi
[ "$actual" = "$expected" ] || { echo "Checksum verification failed" >&2; exit 1; }

tar -xzf "$tmp_dir/$asset" -C "$tmp_dir" rv
install -d "$install_dir"
install -m 755 "$tmp_dir/rv" "$install_dir/rv"
echo "Installed rv to $install_dir/rv"
