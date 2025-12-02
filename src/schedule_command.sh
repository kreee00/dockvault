check_dependencies

SERVICE_NAME="dockvault-backup"
SYSTEMD_DIR="$HOME/.config/systemd/user"
MASTER_SCRIPT="$DOCKVAULT_HOME/master_backup.sh"

echo "${green}== Systemd Scheduler Manager ==${reset}"

if [ ! -f "$MASTER_SCRIPT" ]; then
  echo "${red}Error: Master script not found.${reset}"
  echo "Run 'dockvault generate' first to initialize the workspace."
  exit 1
fi

# ======================================================
# WSL DETECTION
# ======================================================
check_wsl() {
  if grep -qi "microsoft" /proc/version 2>/dev/null; then
    echo ""
    echo "${yellow}--- WSL ENVIRONMENT DETECTED ---${reset}"
    echo "Systemd/Cron are not recommended for background backups in WSL."
    echo ""
    echo "${cyan}Recommended: Use Windows Task Scheduler instead.${reset}"
    echo "Command arguments: -u $USER bash $MASTER_SCRIPT"
    echo ""
    return 0
  fi
  return 1
}

# ======================================================
# CHECK STATUS
# ======================================================
check_status() {
  if check_wsl; then
     echo "${blue}Cannot check status in WSL environment.${reset}"
     return
  fi

  # Check if user systemd is enabled
  if ! systemctl --user daemon-reload 2>/dev/null; then
    echo "${yellow}Warning: User systemd is not available.${reset}"
    echo "You may need to enable lingering: sudo loginctl enable-linger $USER"
    return
  fi

  if systemctl --user list-timers --all 2>/dev/null | grep -q "$SERVICE_NAME"; then
    echo "${green}Status: ACTIVE${reset}"
    echo ""
    echo "--- Timer Info ---"
    systemctl --user list-timers --no-pager 2>/dev/null | grep "$SERVICE_NAME" || echo "Timer not found in list"
    echo ""
    echo "--- Service Status ---"
    systemctl --user status "${SERVICE_NAME}.timer" --no-pager 2>/dev/null | head -n 10
  else
    echo "${yellow}Status: NOT INSTALLED${reset}"
  fi
}

# ======================================================
# INSTALL LOGIC
# ======================================================
install_systemd() {
  # WSL Warning
  if check_wsl; then
    echo ""
    read -p "${red}Force install Systemd on WSL? (May not work) y/N: ${reset}" force
    if [[ "$force" != "y" ]]; then exit 0; fi
  fi

  # Check if user systemd is available
  if ! systemctl --user daemon-reload 2>/dev/null; then
    echo "${red}Error: User systemd is not available!${reset}"
    echo ""
    echo "To enable user systemd, you need to:"
    echo "1. Install and start systemd (on some systems)"
    echo "2. Enable lingering for your user:"
    echo "   sudo loginctl enable-linger $USER"
    echo "3. Log out and log back in"
    echo ""
    echo "Alternatively, use the system-wide option:"
    echo "   sudo dockvault schedule --system"
    exit 1
  fi

  echo "Setting up Systemd Timer..."
  
  # 1. Ensure Directory Exists
  if [ ! -d "$SYSTEMD_DIR" ]; then
      mkdir -p "$SYSTEMD_DIR"
      echo "Created user systemd directory: $SYSTEMD_DIR"
  fi
  
  # 2. Create Service File
  SERVICE_FILE="$SYSTEMD_DIR/${SERVICE_NAME}.service"
  echo "Writing service file to: $SERVICE_FILE"
  
  cat <<EOF > "$SERVICE_FILE"
[Unit]
Description=DockVault Master Backup Service
After=network-online.target docker.service

[Service]
Type=oneshot
ExecStart=/bin/bash $MASTER_SCRIPT
StandardOutput=append:$DOCKVAULT_HOME/logs/systemd_stdout.log
StandardError=append:$DOCKVAULT_HOME/logs/systemd_stderr.log

[Install]
WantedBy=default.target
EOF

  # 3. Create Timer File (Runs daily at 03:00)
  TIMER_FILE="$SYSTEMD_DIR/${SERVICE_NAME}.timer"
  echo "Writing timer file to: $TIMER_FILE"
  
  cat <<EOF > "$TIMER_FILE"
[Unit]
Description=Run DockVault Backup Daily

[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=true

[Install]
WantedBy=timers.target
EOF

  # 4. Activate Changes
  echo "Reloading daemon..."
  systemctl --user daemon-reload
  
  echo "Enabling and Starting timer..."
  systemctl --user enable --now "${SERVICE_NAME}.timer"
  
  if [ $? -eq 0 ]; then
      echo ""
      echo "${green}Success! Timer is now active.${reset}"
      
      # Show next run time
      NEXT_RUN=$(systemctl --user list-timers --no-pager 2>/dev/null | grep "$SERVICE_NAME" | awk '{print $1, $2, $3, $4, $5}' | head -1)
      if [ -n "$NEXT_RUN" ]; then
        echo "${cyan}Next run:${reset} $NEXT_RUN"
      else
        echo "Note: Timer may take a moment to appear in the list"
        echo "Check with: systemctl --user list-timers | grep dockvault"
      fi
      
      echo ""
      echo "${yellow}IMPORTANT TIP:${reset}"
      echo "To ensure backups run even when you are logged out, run:"
      echo "  sudo loginctl enable-linger $USER"
      echo ""
      echo "${blue}MANAGEMENT COMMANDS:${reset}"
      echo "  Check status:  systemctl --user status ${SERVICE_NAME}.timer"
      echo "  View logs:     journalctl --user -u ${SERVICE_NAME}.service"
      echo "  Start now:     systemctl --user start ${SERVICE_NAME}.service"
      echo "  Stop timer:    systemctl --user stop ${SERVICE_NAME}.timer"
  else
      echo "${red}Failed to enable timer.${reset}"
      exit 1
  fi
}

# ======================================================
# SYSTEM-WIDE INSTALL (NEW OPTION)
# ======================================================
install_system_wide() {
  if [ "$EUID" -ne 0 ]; then
    echo "${red}Error: System-wide install requires sudo/root.${reset}"
    echo "Please run: sudo dockvault schedule --system"
    exit 1
  fi
  
  SYSTEMD_SYSTEM_DIR="/etc/systemd/system"
  MASTER_SCRIPT="$DOCKVAULT_HOME/master_backup.sh"
  
  # Verify master script exists
  if [ ! -f "$MASTER_SCRIPT" ]; then
    echo "${red}Error: Master script not found at $MASTER_SCRIPT${reset}"
    exit 1
  fi
  
  echo "Setting up System-wide Systemd Timer (runs as root)..."
  
  # 1. Create Service File
  SERVICE_FILE="$SYSTEMD_SYSTEM_DIR/${SERVICE_NAME}.service"
  echo "Writing service file to: $SERVICE_FILE"
  
  cat <<EOF > "$SERVICE_FILE"
[Unit]
Description=DockVault Master Backup Service
After=network-online.target docker.service

[Service]
Type=oneshot
ExecStart=/bin/bash $MASTER_SCRIPT
StandardOutput=append:$DOCKVAULT_HOME/logs/systemd_stdout.log
StandardError=append:$DOCKVAULT_HOME/logs/systemd_stderr.log

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
OnCalendar=*-*-* 03:00:00
Persistent=true

[Install]
WantedBy=timers.target
EOF

  # 3. Activate Changes
  echo "Reloading daemon..."
  systemctl daemon-reload
  
  echo "Enabling and Starting timer..."
  systemctl enable --now "${SERVICE_NAME}.timer"
  
  if [ $? -eq 0 ]; then
      echo ""
      echo "${green}Success! System-wide timer is now active.${reset}"
      echo ""
      echo "${cyan}Next run:${reset} $(systemctl list-timers --no-pager | grep "$SERVICE_NAME" | awk '{print $1, $2, $3, $4, $5}' | head -1)"
      echo ""
      echo "${blue}MANAGEMENT COMMANDS:${reset}"
      echo "  Check status:  systemctl status ${SERVICE_NAME}.timer"
      echo "  View logs:     journalctl -u ${SERVICE_NAME}.service"
      echo "  Start now:     systemctl start ${SERVICE_NAME}.service"
  else
      echo "${red}Failed to enable timer.${reset}"
      exit 1
  fi
}

# ======================================================
# VIEW LOGS
# ======================================================
view_logs() {
  echo "${blue}Fetching logs from journalctl...${reset}"
  
  if [[ "${args[--system]}" == "1" ]] || [[ "$EUID" -eq 0 ]]; then
    # System logs
    journalctl -u "$SERVICE_NAME" -n 20 --no-pager
  else
    # User logs
    if ! journalctl --user -u "$SERVICE_NAME" -n 20 --no-pager 2>/dev/null; then
      echo "${yellow}No user logs found. Try with sudo for system logs:${reset}"
      echo "  sudo journalctl -u $SERVICE_NAME"
    fi
  fi
}

# ======================================================
# EXECUTION
# ======================================================

if [[ "${args[--install]}" == "1" ]]; then
  install_systemd
elif [[ "${args[--system]}" == "1" ]]; then
  install_system_wide
elif [[ "${args[--logs]}" == "1" ]]; then
  view_logs
elif [[ "${args[--check]}" == "1" ]]; then
  check_status
else
  check_status
  # Prompt install if not active and not on WSL
  if ! check_wsl; then
    if ! systemctl --user list-timers --all 2>/dev/null | grep -q "$SERVICE_NAME"; then
      echo ""
      read -p "Do you want to install Systemd Timer now? (y/N) " choice
      if [[ "$choice" == "y" || "$choice" == "Y" ]]; then
        install_systemd
      fi
    fi
  fi
fi
