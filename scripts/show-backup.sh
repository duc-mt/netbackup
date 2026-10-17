#!/usr/bin/env bash
# ==============================================================================
# Script Name:   show-backup.sh
# Description:   Quickly view or locate the latest configuration backup for a node.
# Usage:         ./show-backup.sh [options] <node-name>
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BACKUP_DIR="${ROOT_DIR}/backups"

print_usage() {
    cat << 'EOF'
Usage:
  ./show-backup.sh [OPTIONS] <node-name>
  ./show-backup.sh -l | --list

Options:
  -d, --diff     Show latest diff instead of full configuration
  -p, --path     Print only file path without opening a pager
  -l, --list     List all devices with existing backups
  -h, --help     Show this help message

Examples:
  ./show-backup.sh NHG-HO-GW-01
  ./show-backup.sh GW-01           (prompts selection if multiple nodes match)
  ./show-backup.sh -d NHG-HO-GW-01
  ./show-backup.sh --path NHG-HO-GW-01
  ./show-backup.sh -l
EOF
}

list_devices() {
    if [[ ! -d "${BACKUP_DIR}" ]]; then
        echo "No backups directory found at ${BACKUP_DIR}" >&2
        exit 1
    fi

    echo "Available nodes in backup history:"
    find "${BACKUP_DIR}" -mindepth 3 -maxdepth 3 -type f -name "*.config" \
        -printf "%f\n" 2>/dev/null \
        | sed 's/\.config$//' \
        | sort -u \
        | sed 's/^/  - /'
}

MODE="config"
PRINT_PATH_ONLY=0
NODE_QUERY=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -d|--diff)
            MODE="diff"
            shift
            ;;
        -p|--path)
            PRINT_PATH_ONLY=1
            shift
            ;;
        -l|--list)
            list_devices
            exit 0
            ;;
        -h|--help)
            print_usage
            exit 0
            ;;
        -*)
            echo "Unknown option: $1" >&2
            print_usage
            exit 1
            ;;
        *)
            if [[ -z "${NODE_QUERY}" ]]; then
                NODE_QUERY="$1"
            else
                echo "Unexpected argument: $1" >&2
                print_usage
                exit 1
            fi
            shift
            ;;
    esac
done

if [[ -z "${NODE_QUERY}" ]]; then
    echo "Error: Node name or substring is required." >&2
    echo "" >&2
    print_usage
    exit 1
fi

if [[ ! -d "${BACKUP_DIR}" ]]; then
    echo "Error: Backups directory '${BACKUP_DIR}' not found." >&2
    exit 1
fi

EXT="${MODE}"

# 1. Discover all unique node names that match the query
ALL_NODES=$(find "${BACKUP_DIR}" -mindepth 3 -maxdepth 3 -type f -name "*.${EXT}" -printf "%f\n" 2>/dev/null \
    | sed "s/\.${EXT}$//" \
    | sort -u)

MATCHING_NODES=$(echo "${ALL_NODES}" | grep -i -- "${NODE_QUERY}" || true)

if [[ -z "${MATCHING_NODES}" ]]; then
    echo "No ${EXT} files found matching '${NODE_QUERY}' in ${BACKUP_DIR}." >&2
    echo "Run './show-backup.sh -l' to view all available devices." >&2
    exit 1
fi

SELECTED_NODE=""
# Check if there is an exact case-insensitive match
EXACT_MATCH=$(echo "${MATCHING_NODES}" | grep -i -x -- "${NODE_QUERY}" || true)

if [[ -n "${EXACT_MATCH}" ]]; then
    SELECTED_NODE="${EXACT_MATCH}"
else
    # Count matching candidates
    MATCH_COUNT=$(echo "${MATCHING_NODES}" | wc -l)
    if [[ ${MATCH_COUNT} -eq 1 ]]; then
        SELECTED_NODE="${MATCHING_NODES}"
    else
        # Multiple matches found
        mapfile -t CANDIDATES < <(echo "${MATCHING_NODES}")

        # If running non-interactively or just asking for path without a tty
        if [[ ! -t 0 ]] || [[ ${PRINT_PATH_ONLY} -eq 1 && ! -t 1 ]]; then
            echo "Error: Multiple nodes match '${NODE_QUERY}':" >&2
            for item in "${CANDIDATES[@]}"; do
                echo "  - ${item}" >&2
            done
            echo "Please be more specific." >&2
            exit 1
        fi

        echo "Multiple nodes match '${NODE_QUERY}':"
        for i in "${!CANDIDATES[@]}"; do
            printf "  [%d] %s\n" "$((i + 1))" "${CANDIDATES[$i]}"
        done

        while true; do
            read -r -p "Select a node [1-${#CANDIDATES[@]}] (or 'q' to quit): " CHOICE
            if [[ "${CHOICE}" == "q" || "${CHOICE}" == "Q" ]]; then
                echo "Aborted."
                exit 0
            fi
            if [[ "${CHOICE}" =~ ^[0-9]+$ ]] && (( CHOICE >= 1 && CHOICE <= ${#CANDIDATES[@]} )); then
                SELECTED_NODE="${CANDIDATES[$((CHOICE - 1))]}"
                break
            fi
            echo "Invalid selection. Please enter a number between 1 and ${#CANDIDATES[@]}."
        done
    fi
fi

# 2. Find the newest file for the selected node
LATEST_FILE=$(find "${BACKUP_DIR}" -mindepth 3 -maxdepth 3 -type f -name "${SELECTED_NODE}.${EXT}" -printf "%T@ %p\n" 2>/dev/null \
    | sort -nr \
    | head -n 1 \
    | cut -d' ' -f2-)

if [[ -z "${LATEST_FILE}" || ! -f "${LATEST_FILE}" ]]; then
    echo "No ${EXT} file found for node '${SELECTED_NODE}'." >&2
    exit 1
fi

if [[ ${PRINT_PATH_ONLY} -eq 1 ]]; then
    echo "${LATEST_FILE}"
    exit 0
fi

# 3. Display file
echo "==> Latest ${MODE} for '${SELECTED_NODE}': ${LATEST_FILE}"

if [[ ! -t 1 ]]; then
    cat "${LATEST_FILE}"
elif [[ -n "${PAGER:-}" ]]; then
    ${PAGER} "${LATEST_FILE}"
elif command -v less >/dev/null 2>&1; then
    less -R "${LATEST_FILE}"
else
    cat "${LATEST_FILE}"
fi
