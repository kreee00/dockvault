  local path="${1:-}"
  local remote="${2:-dockvault-scadadb}"
  
  echo "Listing files from remote: $remote"
  echo "Path: ${path:-/ (root)}"
  echo "----------------------------------------"
  
  # Check if rclone is installed
  if ! command -v rclone &> /dev/null; then
    echo "Error: rclone is not installed or not in PATH" >&2
    exit 1
  fi
  
  # Build the rclone command
  local rclone_cmd="rclone ls \"$remote:$path\""
  
  # Execute the command
  if ! eval "$rclone_cmd"; then
    echo "Error: Failed to list files from remote '$remote'" >&2
    echo "Make sure the remote is configured and accessible" >&2
    exit 1
  fi
