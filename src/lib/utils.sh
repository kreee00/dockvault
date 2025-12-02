## Colors
green=$(tput setaf 2)
red=$(tput setaf 1)
yellow=$(tput setaf 3)
blue=$(tput setaf 4)
cyan=$(tput setaf 6)
reset=$(tput sgr0)

## Dependency Check
check_dependencies() {
  local dependencies=("docker" "rclone" "tar" "gzip")
  for cmd in "${dependencies[@]}"; do
    if ! command -v "$cmd" &> /dev/null; then
      echo "${red}Error: Required command '$cmd' is not installed.${reset}"
      exit 1
    fi
  done
}

## Selectors
select_volume() {
  echo "${blue}Scanning Docker volumes...${reset}" >&2
  local volumes=($(docker volume ls -q))
  if [ ${#volumes[@]} -eq 0 ]; then
    echo "${red}No docker volumes found!${reset}" >&2
    exit 1
  fi
  PS3="${yellow}Select target volume: ${reset}"
  select vol in "${volumes[@]}"; do
    if [[ -n "$vol" ]]; then echo "$vol"; break; fi
  done < /dev/tty
}

select_container() {
  echo "${blue}Scanning running containers...${reset}" >&2
  local containers=($(docker ps --format "{{.Names}}"))
  if [ ${#containers[@]} -eq 0 ]; then
    echo "${red}No running containers found!${reset}" >&2
    exit 1
  fi
  PS3="${yellow}Select container: ${reset}"
  select cont in "${containers[@]}"; do
    if [[ -n "$cont" ]]; then echo "$cont"; break; fi
  done < /dev/tty
}

## Master Script Manager
update_master_script() {
  local master_file="${DOCKVAULT_HOME}/master_backup.sh"
  
  # specific script to register
  local new_script_path="$1"
  
  # Create master if not exists
  if [ ! -f "$master_file" ]; then
    cat <<EOF > "$master_file"
#!/bin/bash
# Master Backup Script - Managed by DockVault
# Add this file to crontab

LOG_DIR="${DOCKVAULT_HOME}/logs"
mkdir -p "\$LOG_DIR"
TODAY=\$(date +"%Y-%m-%d")
MASTER_LOG="\$LOG_DIR/master_\$TODAY.log"

echo "[START] Master Backup started at \$(date)" >> "\$MASTER_LOG"

EOF
    chmod +x "$master_file"
  fi

  # Check if entry exists to avoid duplicates
  if ! grep -Fq "$new_script_path" "$master_file"; then
    echo "bash \"$new_script_path\" >> \"\$MASTER_LOG\" 2>&1" >> "$master_file"
    echo "${green}Added to master_backup.sh${reset}"
  else
    echo "${yellow}Script already registered in master_backup.sh${reset}"
  fi
}
