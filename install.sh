#!/bin/sh
set -eu

repo="${KAPI_REPOSITORY:-xaaha/kapi}"
api="${KAPI_API_URL:-https://api.github.com/repos/$repo}"

fail() {
  printf 'kapi installer: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

need curl
need tar

os=$(uname -s)
case "$os" in
  Darwin|Linux) ;;
  *) fail "unsupported operating system: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=x86_64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac

version=${KAPI_VERSION:-}
if [ -z "$version" ]; then
  version=$(curl -fsSL "$api/releases/latest" | awk -F '"' '/"tag_name"/ { print $4; exit }')
  [ -n "$version" ] || fail "could not determine the latest release"
fi
case "$version" in
  v*) tag=$version; number=${version#v} ;;
  *) tag=v$version; number=$version ;;
esac

archive="kapi_${number}_${os}_${arch}.tar.gz"
base="${KAPI_RELEASE_BASE_URL:-https://github.com/$repo/releases/download/$tag}"
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t kapi)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

printf 'Downloading kapi %s for %s/%s...\n' "$tag" "$os" "$arch"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

expected=$(awk -v file="$archive" '$2 == file || $2 == "*" file { print $1; exit }' "$tmp/checksums.txt")
[ -n "$expected" ] || fail "$archive is missing from checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
  fail "sha256sum or shasum is required to verify the release"
fi
[ "$actual" = "$expected" ] || fail "checksum verification failed for $archive"

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/kapi" ] || fail "$archive did not contain the kapi binary"

if [ -n "${KAPI_INSTALL_DIR:-}" ]; then
  install_dir=$KAPI_INSTALL_DIR
  custom_dir=1
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
  install_dir=/usr/local/bin
  custom_dir=0
else
  install_dir=$HOME/.local/bin
  custom_dir=0
fi
mkdir -p "$install_dir"
cp "$tmp/kapi" "$install_dir/kapi"
chmod 755 "$install_dir/kapi"

case ":${PATH:-}:" in
  *":$install_dir:"*) ;;
  *)
    if [ "$custom_dir" -eq 0 ] && [ "$install_dir" = "$HOME/.local/bin" ] && [ "${KAPI_NO_PATH_UPDATE:-0}" != 1 ]; then
      case "${SHELL:-}" in
        */zsh) profile=$HOME/.zprofile ;;
        *) profile=$HOME/.profile ;;
      esac
      line='export PATH="$HOME/.local/bin:$PATH"'
      if ! grep -F "$line" "$profile" >/dev/null 2>&1; then
        printf '\n# Added by the kapi installer\n%s\n' "$line" >>"$profile"
      fi
      PATH="$install_dir:$PATH"
      export PATH
      printf 'Added %s to PATH in %s.\n' "$install_dir" "$profile"
    else
      printf 'Installed to %s, which is not on PATH. Add it to your shell PATH.\n' "$install_dir" >&2
    fi
    ;;
esac

printf 'Installed %s.\n' "$install_dir/kapi"
if [ "${KAPI_SKIP_SETUP:-0}" != 1 ]; then
  printf 'Connecting kapi to your coding agents...\n'
  if [ -r /dev/tty ]; then
    "$install_dir/kapi" setup </dev/tty
  else
    "$install_dir/kapi" setup </dev/null
  fi
fi
