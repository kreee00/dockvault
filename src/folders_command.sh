# src/list/folders_command.sh

# 1. Get arguments and flags
local path="${1:-}"
local remote="${2:-dockvault-scadadb}"

# 2. Validate rclone
if ! command -v rclone &> /dev/null; then
  echo "Error: rclone is not installed." >&2
  exit 1
fi

# 3. Visual Output
echo "Generating folder hierarchy for: $remote:$path"
echo "----------------------------------------"

# 4. Run rclone tree with --dirs-only
# This provides the hierarchy visual you requested
if ! rclone tree "$remote:$path" --dirs-only; then
  echo "Error: Failed to generate tree view." >&2
  exit 1
fi
