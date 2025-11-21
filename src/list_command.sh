# src/list_command.sh

# 1. Get arguments and flags
local path="${1:-}"
local remote="${2:-dockvault-scadadb}"

# 2. Validate rclone
if ! command -v rclone &> /dev/null; then
  echo "Error: rclone is not installed." >&2
  exit 1
fi

# 3. Visual Output
echo "Listing files in: $remote:$path"
echo "----------------------------------------"

# 4. Run rclone ls (Lists size and path of objects)
# You can also use 'rclone lsl' for long listing with times
if ! rclone ls "$remote:$path"; then
  echo "Error: Failed to list files." >&2
  exit 1
fi
