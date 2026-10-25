#!/bin/sh
# Ограничение размера исходных файлов (openspec spec source-file-size):
# production-код не больше 300 строк, тесты не больше 500. Проверяются только
# отслеживаемые git файлы; сгенерированный код, зависимости, сборка и fixtures
# исключены. Исключение — строка "<path> <reason>" в file-size-allowlist.txt.
# Использование: ops/checks/file-size.sh [path...] (по умолчанию boundaries).
set -eu
cd "$(git rev-parse --show-toplevel)"
allowlist="ops/checks/file-size-allowlist.txt"
[ "$#" -gt 0 ] || set -- boundaries

git ls-files -- "$@" |
  grep -E '\.(go|rs|ts|tsx|py)$' |
  grep -vE '(^|/)(node_modules|target[^/]*|dist|\.venv|__pycache__|gen|generated|fixtures)/' |
  grep -vE '(\.pb\.go|_grpc\.pb\.go|wire_gen\.go|_pb\.js|_pb\.d\.ts|\.d\.ts)$' |
  while IFS= read -r file; do
    [ -f "$file" ] || continue
    printf '%s %s\n' "$(wc -l <"$file" | tr -d ' ')" "$file"
  done |
  awk -v allowlist="$allowlist" '
    BEGIN {
      while ((getline line < allowlist) > 0) {
        if (line ~ /^[[:space:]]*(#|$)/) continue
        split(line, part, " ")
        allowed[part[1]] = 1
      }
    }
    {
      lines = $1; file = $2
      test = file ~ /(_test\.go|\.test\.tsx?|\/tests?\/|\/tests\.rs$|\/benches\/|\/test_[^\/]*\.py$)/
      limit = test ? 500 : 300
      if (lines > limit && !(file in allowed)) { printf "%5d > %d  %s\n", lines, limit, file; failed = 1 }
    }
    END { exit failed }
  '
