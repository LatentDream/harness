default:
    just --list

build:
    go build .

run:
    go run .

dev:
    go run .

test:
    go test ./...

_latest-log:
    #!/usr/bin/env bash
    set -euo pipefail
    shopt -s nullglob

    logs=("$HOME"/.harness/logs/*/*.log)
    if [ ${#logs[@]} -eq 0 ]; then
        printf 'no harness logs found under %s\n' "$HOME/.harness/logs" >&2
        exit 1
    fi

    latest=""
    latest_mtime=0
    for log in "${logs[@]}"; do
        mtime=$(stat -c %Y "$log")
        if [ "$mtime" -gt "$latest_mtime" ]; then
            latest="$log"
            latest_mtime="$mtime"
        fi
    done


    printf '%s\n' "$latest"

[group('debug')]
log:
    #!/usr/bin/env bash
    set -euo pipefail

    latest=$(just _latest-log)
    printf 'tailing %s\n' "$latest"
    tail -n +1 -f "$latest" | jq --unbuffered .

[group('debug')]
log-text:
    #!/usr/bin/env bash
    set -euo pipefail

    latest=$(just _latest-log)
    printf 'tailing %s\n' "$latest"
    tail -n +1 -f "$latest" | jq --unbuffered -r '
        def color($level):
            if $level == "DEBUG" or $level == "debug" then "\u001b[36m"
            elif $level == "INFO" or $level == "info" then "\u001b[32m"
            elif $level == "WARN" or $level == "WARNING" or $level == "warn" or $level == "warning" then "\u001b[33m"
            elif $level == "ERROR" or $level == "error" then "\u001b[31m"
            elif $level == "FATAL" or $level == "fatal" then "\u001b[35;1m"
            else ""
            end;
        def reset: "\u001b[0m";
        def leafs($path):
            if type == "object" then
                to_entries[] as $entry | $entry.value | leafs($path + [$entry.key])
            elif type == "array" then
                to_entries[] as $entry | $entry.value | leafs($path + [($entry.key | tostring)])
            else
                [($path | join(".")), .]
            end;
        "---",
        ((.level // .L // "") as $level | ([.level // .L, .ts // .T, .caller // .C, .msg // .M] | map(select(. != null)) | join(" ")) as $header | "\(color($level))\($header)\(reset)"),
        (leafs([]) as $leaf | select((["level", "ts", "caller", "msg", "L", "T", "C", "M"] | index($leaf[0]) | not)) | "\($leaf[0]):\n\($leaf[1])")
    '
