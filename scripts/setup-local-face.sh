#!/usr/bin/env sh
# Configure an existing, architecture-matched Linux InspireFace runtime and
# model pack. This script neither changes the SDK nor downloads private models.
set -eu
if [ "$#" -ne 2 ]; then
  printf 'Usage: %s /absolute/path/libInspireFace.so /absolute/path/Megatron\n' "$0" >&2
  exit 2
fi
library=$(realpath "$1")
pack=$(realpath "$2")
test -r "$library" && test -r "$pack"
case "$(uname -m)" in
  x86_64|aarch64) ;;
  *) printf 'Supported Linux architectures: x86_64, aarch64\n' >&2; exit 2 ;;
esac
if ldd "$library" | grep -q 'not found'; then
  ldd "$library" >&2
  printf 'Install the missing native runtime dependencies first.\n' >&2
  exit 2
fi
printf 'FACE_ENGINE_LIBRARY_PATH=%s\nFACE_MODEL_PACK_PATH=%s\nFACE_DETECT_PIXEL_SIZE=320\n' "$library" "$pack"
printf 'Build with CGO_ENABLED=1. Docker uses the Debian/glibc image, not Alpine.\n' >&2
