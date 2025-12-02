check_dependencies

echo "${green}== Manual Backup Trigger ==${reset}"
echo "Workspace: $DOCKVAULT_HOME"

# 1. Find Managed Volumes
managed_vols=()
for d in "$DOCKVAULT_HOME"/*/; do
  # Check if directory and contains backup.sh
  if [ -f "$d/backup.sh" ]; then
    managed_vols+=("$(basename "$d")")
  fi
done

if [ ${#managed_vols[@]} -eq 0 ]; then
  echo "${red}No managed backup scripts found.${reset}"
  echo "Use 'dockvault generate' to setup a volume first."
  exit 1
fi

# 2. Display Options
echo ""
echo "Available Backup Jobs:"
i=0
for vol in "${managed_vols[@]}"; do
  i=$((i + 1))
  echo "  [$i] $vol"
done
echo "  [A] Run ALL (Master Backup)"
echo ""

# 3. Capture Input
read -p "${yellow}Enter selection (e.g. '1 3' for multiple, or 'A' for all): ${reset}" selection

# 4. Process Selection
selected_scripts=()
run_master=false

# Convert input to array (space separated)
read -ra INDICES <<< "$selection"

for idx in "${INDICES[@]}"; do
  if [[ "$idx" == "A" ]] || [[ "$idx" == "a" ]]; then
    run_master=true
    break
  elif [[ "$idx" =~ ^[0-9]+$ ]]; then
    # Adjust for 0-based array vs 1-based display
    array_idx=$((idx - 1))
    
    if [ $array_idx -ge 0 ] && [ $array_idx -lt ${#managed_vols[@]} ]; then
      vol_name="${managed_vols[$array_idx]}"
      selected_scripts+=("$DOCKVAULT_HOME/$vol_name/backup.sh")
    else
      echo "${red}Warning: Invalid index '$idx' ignored.${reset}"
    fi
  else
    echo "${red}Warning: Invalid input '$idx' ignored.${reset}"
  fi
done

# 5. Execute
echo ""
echo "${blue}--- Execution Plan ---${reset}"

if [ "$run_master" = true ]; then
  master_script="$DOCKVAULT_HOME/master_backup.sh"
  if [ ! -f "$master_script" ]; then
    echo "${red}Error: Master script not found at $master_script${reset}"
    exit 1
  fi
  
  echo "Target: MASTER BACKUP (All Volumes)"
  
  if [[ "${args[--dry-run]}" == "1" ]]; then
    echo "${cyan}[DRY-RUN] Would execute: bash $master_script${reset}"
  else
    echo "Launching Master Backup..."
    echo "------------------------------------------------"
    bash "$master_script"
  fi

elif [ ${#selected_scripts[@]} -gt 0 ]; then
  echo "Targets: ${#selected_scripts[@]} volume(s)"
  
  for script in "${selected_scripts[@]}"; do
    if [[ "${args[--dry-run]}" == "1" ]]; then
      echo "${cyan}[DRY-RUN] Would execute: bash $script${reset}"
    else
      echo "Running: $(basename "$(dirname "$script")") ..."
      bash "$script"
      echo "------------------------------------------------"
    fi
  done
else
  echo "No valid selection made. Aborting."
  exit 0
fi

if [[ "${args[--dry-run]}" != "1" ]]; then
  echo ""
  echo "${green}Batch Completed.${reset}"
fi
