#!/usr/bin/env bash
# Shared helper: parse hook JSON input.
# Sources this file to get parse_file_path() and parse_command().
# Uses jq if available, falls back to grep+sed.

_parse_json_field() {
    local input="$1" field="$2"
    if command -v jq &>/dev/null; then
        echo "$input" | jq -r --arg f "$field" '
            if .tool_input[$f] then .tool_input[$f]
            elif .[$f] then .[$f]
            else empty end
        ' 2>/dev/null
    else
        echo "$input" | grep -o "\"$field\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | sed "s/\"$field\"[[:space:]]*:[[:space:]]*\"//;s/\"$//"
    fi
}

parse_file_path() {
    _parse_json_field "$1" "file_path"
}

parse_command() {
    _parse_json_field "$1" "command"
}
