#!/usr/bin/env bash
# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.


# Helpers shared by setup.sh and its regression tests. The Trustant Code
# checkout can be mounted into Lima without the host-side Git directory that a
# nested submodule .git file references, so source fingerprinting must not
# require Git metadata.

trustant_sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
    return
  fi
  return 127
}

trustant_file_sha256() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return
  fi
  return 127
}

trustant_file_mode() {
  local file="$1"
  stat -c '%a' "$file" 2>/dev/null || stat -f '%Lp' "$file" 2>/dev/null
}

trustant_code_source_hash() {
  local root="$1"
  local file relative mode digest
  [[ -d "$root" ]] || return 1

  (
    while IFS= read -r -d '' file; do
      relative="${file#"$root"/}"
      if [[ -L "$file" ]]; then
        mode="symlink"
        digest=$(printf '%s' "$(readlink "$file")" | trustant_sha256_stream) || exit 1
      else
        mode=$(trustant_file_mode "$file") || exit 1
        digest=$(trustant_file_sha256 "$file") || exit 1
      fi
      printf '%s\0%s\0%s\n' "$relative" "$mode" "$digest"
    done < <(
      find "$root" \
        \( -name .git -o -name node_modules -o -name dist -o -name .turbo -o -name .cache -o -name coverage \) -prune -o \
        \( -type f -o -type l \) \
        ! -name '*.bun-build' \
        ! -name '.DS_Store' \
        ! -path "$root/.opencode/package.json" \
        ! -path "$root/.opencode/package-lock.json" \
        -print0 | sort -z
    )
  ) | trustant_sha256_stream
}

trustant_code_source_identity() {
  local root="$1"
  local ref="${TRUSTANT_CODE_SOURCE_REF:-}"
  local source_hash

  [[ -f "$root/packages/opencode/package.json" ]] || return 2

  if [[ -z "$ref" ]]; then
    ref=$(git -C "$root" rev-parse --verify HEAD 2>/dev/null || true)
  fi
  if [[ -n "$ref" && ! "$ref" =~ ^[0-9a-fA-F]{40,64}$ ]]; then
    return 3
  fi

  source_hash=$(trustant_code_source_hash "$root") || return 4
  [[ -n "$source_hash" ]] || return 4
  [[ -n "$ref" ]] || ref="source-${source_hash:0:12}"

  printf '%s\t%s\n' "$ref" "$source_hash"
}
