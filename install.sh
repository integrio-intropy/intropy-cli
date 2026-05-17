#!/bin/sh
# install.sh — install intropy from GitHub Releases
#
# Usage:
#   curl -fsSL https://get.intropy.example/install.sh | sh
#   curl -fsSL https://get.intropy.example/install.sh | sh -s -- --version v0.1.0
#   curl -fsSL https://get.intropy.example/install.sh | sh -s -- --prefix ~/.local

set -e

OWNER="intropy"
REPO="intropy-cli"
BINARY="intropy"
GITHUB="https://github.com/${OWNER}/${REPO}"

# Defaults
PREFIX=""
VERSION=""

# Parse arguments passed via sh -s -- ...
while [ "$#" -gt 0 ]; do
	case "$1" in
		--version)
			VERSION="$2"
			shift 2
			;;
		--prefix)
			PREFIX="$2"
			shift 2
			;;
		-h|--help)
			echo "Usage: install.sh [--version VERSION] [--prefix PREFIX]"
			echo ""
			echo "Options:"
			echo "  --version VERSION   Install a specific version (default: latest)"
			echo "  --prefix PREFIX     Install directory (default: /usr/local/bin or ~/.local/bin)"
			exit 0
			;;
		*)
			echo "Unknown option: $1"
			exit 1
			;;
	esac
done

# --- detect OS ---
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
	linux) OS="Linux" ;;
	darwin) OS="Darwin" ;;
	*)
		echo "Unsupported operating system: $OS"
		echo "Supported: Linux, Darwin (macOS)"
		exit 1
		;;
esac

# --- detect architecture ---
ARCH=$(uname -m)
case "$ARCH" in
	x86_64) ARCH="amd64" ;;
	aarch64|arm64) ARCH="arm64" ;;
	*)
		echo "Unsupported architecture: $ARCH"
		echo "Supported: amd64, arm64"
		exit 1
		;;
esac

# --- resolve version ---
if [ -z "$VERSION" ]; then
	# Fetch the latest release tag from GitHub API
	echo "==> Resolving latest version..."
	VERSION=$(curl -fsSL "${GITHUB}/releases/latest" | sed -n 's/.*tag_name":"\([^"]*\)".*/\1/p')
	if [ -z "$VERSION" ]; then
		echo "Failed to determine latest version"
		exit 1
	fi
fi

echo "==> Installing ${BINARY} ${VERSION} for ${OS}/${ARCH}..."

# --- determine install directory ---
if [ -z "$PREFIX" ]; then
	# Try /usr/local/bin first, fall back to ~/.local/bin
	if [ -w "/usr/local/bin" ] 2>/dev/null || mkdir -p "/usr/local/bin" 2>/dev/null; then
		PREFIX="/usr/local/bin"
	else
		PREFIX="${HOME}/.local/bin"
		mkdir -p "$PREFIX"
	fi
fi

if [ ! -d "$PREFIX" ]; then
	mkdir -p "$PREFIX" || {
		echo "Failed to create directory: $PREFIX"
		exit 1
	}
fi

echo "==> Install directory: $PREFIX"

# --- download artifact ---
ARTIFACT="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

BASE_URL="${GITHUB}/releases/download/${VERSION}"

echo "==> Downloading ${ARTIFACT}..."
curl -fsSL "${BASE_URL}/${ARTIFACT}" -o "${TMPDIR}/${ARTIFACT}"

# --- download and verify checksum ---
echo "==> Verifying checksum..."
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMPDIR}/checksums.txt"
cd "$TMPDIR"

# Extract the expected checksum for our artifact
EXPECTED=$(grep "${ARTIFACT}" checksums.txt | awk '{print $1}')
if [ -z "$EXPECTED" ]; then
	echo "Artifact not found in checksums.txt"
	exit 1
fi

# Compute actual checksum
ACTUAL=$(sha256sum "${ARTIFACT}" | awk '{print $1}')
if [ "$EXPECTED" != "$ACTUAL" ]; then
	echo "Checksum mismatch!"
	echo "  expected: ${EXPECTED}"
	echo "  actual:   ${ACTUAL}"
	exit 1
fi

echo "==> Checksum OK"

# --- optional cosign verification ---
if command -v cosign >/dev/null 2>&1; then
	echo "==> Verifying signature with cosign..."
	curl -fsSL "${BASE_URL}/${ARTIFACT}.sig" -o "${TMPDIR}/${ARTIFACT}.sig"
	curl -fsSL "${BASE_URL}/${ARTIFACT}.pem" -o "${TMPDIR}/${ARTIFACT}.pem"
	cosign verify-blob \
		--certificate "${TMPDIR}/${ARTIFACT}.pem" \
		--signature "${TMPDIR}/${ARTIFACT}.sig" \
		--certificate-identity-regexp="https://github.com/${OWNER}/${REPO}" \
		--certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
		"${TMPDIR}/${ARTIFACT}" || {
		echo "Cosign verification failed"
		exit 1
	}
	echo "==> Signature verified"
else
	echo "==> cosign not found, skipping signature verification"
	echo "    (install cosign to verify: https://docs.sigstore.dev/cosign/installation/)"
fi

# --- extract and install ---
echo "==> Extracting..."
tar -xzf "${TMPDIR}/${ARTIFACT}" -C "$TMPDIR"

# The binary should be at TMPDIR/intropy or TMPDIR/bin/intropy
if [ -f "${TMPDIR}/${BINARY}" ]; then
	BINARY_PATH="${TMPDIR}/${BINARY}"
elif [ -f "${TMPDIR}/bin/${BINARY}" ]; then
	BINARY_PATH="${TMPDIR}/bin/${BINARY}"
else
	echo "Binary not found in archive"
	exit 1
fi

echo "==> Installing ${BINARY} to ${PREFIX}..."
if [ -w "$PREFIX" ]; then
	install -m 755 "$BINARY_PATH" "${PREFIX}/${BINARY}"
else
	echo "    (sudo required for ${PREFIX})"
	sudo install -m 755 "$BINARY_PATH" "${PREFIX}/${BINARY}"
fi

# --- install shell completions ---
if command -v "${PREFIX}/${BINARY}" >/dev/null 2>&1; then
	echo "==> Installing shell completions..."

	# Bash
	if [ -d "/etc/bash_completion.d" ] && [ -w "/etc/bash_completion.d" ]; then
		"${PREFIX}/${BINARY}" completion bash > "/etc/bash_completion.d/${BINARY}"
		echo "    bash: /etc/bash_completion.d/${BINARY}"
	elif [ -d "/usr/local/etc/bash_completion.d" ] && [ -w "/usr/local/etc/bash_completion.d" ]; then
		"${PREFIX}/${BINARY}" completion bash > "/usr/local/etc/bash_completion.d/${BINARY}"
		echo "    bash: /usr/local/etc/bash_completion.d/${BINARY}"
	fi

	# Zsh
	if [ -d "/usr/local/share/zsh/site-functions" ] && [ -w "/usr/local/share/zsh/site-functions" ]; then
		"${PREFIX}/${BINARY}" completion zsh > "/usr/local/share/zsh/site-functions/_${BINARY}"
		echo "    zsh: /usr/local/share/zsh/site-functions/_${BINARY}"
	elif [ -d "${HOME}/.zsh/completions" ]; then
		mkdir -p "${HOME}/.zsh/completions"
		"${PREFIX}/${BINARY}" completion zsh > "${HOME}/.zsh/completions/_${BINARY}"
		echo "    zsh: ~/.zsh/completions/_${BINARY}"
	fi

	# Fish
	if [ -d "${HOME}/.config/fish/completions" ]; then
		mkdir -p "${HOME}/.config/fish/completions"
		"${PREFIX}/${BINARY}" completion fish > "${HOME}/.config/fish/completions/${BINARY}.fish"
		echo "    fish: ~/.config/fish/completions/${BINARY}.fish"
	fi
fi

# --- verify installation ---
echo ""
echo "==> ${BINARY} installed successfully!"
"${PREFIX}/${BINARY}" version

# --- PATH warning ---
case ":${PATH}:" in
	*":${PREFIX}:"*) ;;
	*)
		echo ""
		echo "WARNING: ${PREFIX} is not in your PATH."
		echo "Add the following to your shell profile:"
		echo "  export PATH=\"${PREFIX}:\${PATH}\""
		;;
esac
