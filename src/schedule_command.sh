check_dependencies

SERVICE_NAME="dockvault-backup"
SYSTEMD_SYSTEM_DIR="/etc/systemd/system"

# Get the REAL user if running as sudo, otherwise use current user
TARGET_USER="${SUDO_USER:-$USER}"
TARGET_HOME=$(getent passwd "$TARGET_USER" | cut -d: -f6)
REAL_MASTER_SCRIPT="$TARGET_HOME/dockvault_scripts/master_backup.sh"
LOG_DIR="$TARGET_HOME/dockvault_scripts/logs"

echo "${green}== Systemd Scheduler Manager (System-Wide) ==${reset}"

if [ ! -f "$REAL_MASTER_SCRIPT" ]; then
  echo "${red}Error: Master script not found at ${REAL_MASTER_SCRIPT}.${reset}"
  echo "Run 'dockvault generate' first to initialize the workspace for user '$TARGET_USER'."
  exit 1
fi

# ======================================================
# CHECK STATUS
# ======================================================
check_status() {
  # Check system-wide timers (requires sudo usually, but list-timers might show all)
  if systemctl list-timers --all 2>/dev/null | grep -q "$SERVICE_NAME"; then
    echo "${green}Status: ACTIVE (System-Wide)${reset}"
    echo ""
    echo "--- Timer Info ---"
    systemctl list-timers --no-pager | grep "$SERVICE_NAME"
    echo ""
    echo "--- Service Status ---"
    systemctl status "${SERVICE_NAME}.timer" --no-pager | head -n 10
  else
    echo "${yellow}Status: NOT INSTALLED${reset}"
  fi
}

# ======================================================
# INSTALL LOGIC
# ======================================================
install_system_wide() {
  # Check for sudo/root permissions
  if [ "$EUID" -ne 0 ]; then
    echo "${red}Error: Installing system-wide services requires root privileges.${reset}"
    echo "Please run this command with sudo:"
    echo "  sudo dockvault schedule --install"
    exit 1
  fi

  echo "Setting up System-Wide Systemd Timer..."
  
  # Get the REAL user if running as sudo
  # If SUDO_USER is set, use it; otherwise use current USER
  TARGET_USER="${SUDO_USER:-$USER}"
  
  # We need the absolute path to the user's home if running as sudo
  # getent passwd $TARGET_USER | cut -d: -f6 -> returns /home/username
  TARGET_HOME=$(getent passwd "$TARGET_USER" | cut -d: -f6)
  
  # Re-evaluate DOCKVAULT_HOME based on target user if it points to /root
  # (This handles the case where sudo dockvault was run, confusing $HOME)
  REAL_MASTER_SCRIPT="$TARGET_HOME/dockvault_scripts/master_backup.sh"
  LOG_DIR="$TARGET_HOME/dockvault_scripts/logs"

  if [ ! -f "$REAL_MASTER_SCRIPT" ]; then
     echo "${red}Error: Could not locate master script at $REAL_MASTER_SCRIPT${reset}"
     echo "Ensure you generated the scripts as user '$TARGET_USER' first."
     exit 1
  fi

  # 1. Create Service File in /etc/systemd/system
  SERVICE_FILE="$SYSTEMD_SYSTEM_DIR/${SERVICE_NAME}.service"
  echo "Writing service file to: $SERVICE_FILE"
  
  cat <<EOF > "$SERVICE_FILE"
[Unit]
Description=DockVault Master Backup Service (User: $TARGET_USER)
After=network-online.target docker.service
Requires=docker.service

[Service]
Type=simple
User=$TARGET_USER
Environment="DOCKVAULT_HOME=$TARGET_HOME/dockvault_scripts"
ExecStart=/bin/bash $REAL_MASTER_SCRIPT
StandardOutput=append:$LOG_DIR/systemd_stdout.log
StandardError=append:$LOG_DIR/systemd_stderr.log

[Install]
WantedBy=multi-user.target
EOF

  # 2. Create Timer File
  TIMER_FILE="$SYSTEMD_SYSTEM_DIR/${SERVICE_NAME}.timer"
  echo "Writing timer file to: $TIMER_FILE"
  
  cat <<EOF > "$TIMER_FILE"
[Unit]
Description=Run DockVault Backup Daily

[Timer]
OnCalendar=*-*-* 01:00:00
Persistent=true

[Install]
WantedBy=timers.target
EOF

  # 3. Activate Changes
  echo "Reloading system daemon..."
  systemctl daemon-reload
  
  echo "Enabling and Starting timer..."
  systemctl enable --now "${SERVICE_NAME}.timer"
  
  if [ $? -eq 0 ]; then
      echo ""
      echo "${green}Success! System-wide timer is active.${reset}"
      echo "Runs as user: ${cyan}$TARGET_USER${reset}"
      echo "Script path:  ${cyan}$REAL_MASTER_SCRIPT${reset}"
      echo ""
      echo "${cyan}Next run:${reset} $(systemctl list-timers --no-pager | grep "$SERVICE_NAME" | awk '{print $2, $3}')"
  else
      echo "${red}Failed to enable timer.${reset}"
      exit 1
  fi
}

# ======================================================
# VIEW LOGS
# ======================================================
view_logs() {
  echo "${blue}Fetching system logs for service...${reset}"
  # System logs usually require sudo to view fully, or being in 'systemd-journal' group
  journalctl -u "$SERVICE_NAME" -n 20 --no-pager || echo "${yellow}Permission denied. Try running with sudo.${reset}"
}

# ======================================================
# EXECUTION
# ======================================================

if [[ "${args[--install]}" == "1" ]]; then
  install_system_wide
elif [[ "${args[--logs]}" == "1" ]]; then
  view_logs
elif [[ "${args[--check]}" == "1" ]]; then
  check_status
else
  check_status
  
  # Check for WSL (Windows Subsystem for Linux)
  if grep -qi "microsoft" /proc/version 2>/dev/null; then
      echo "${yellow}Note: WSL detected. Systemd support requires WSL2 and recent updates.${reset}"
  fi

  if ! systemctl list-timers --all 2>/dev/null | grep -q "$SERVICE_NAME"; then
      echo ""
      echo "Timer not found."
      echo "To install system-wide, run:"
      echo "${cyan}sudo dockvault schedule --install${reset}"
  fi
fi