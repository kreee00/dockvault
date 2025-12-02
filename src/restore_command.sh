check_dependencies

echo "${green}== Restore Navigator ==${reset}"
echo "Searching for managed restore scripts in $DOCKVAULT_HOME..."

# Find folders that contain restore.sh
managed_vols=()
for d in "$DOCKVAULT_HOME"/*/; do
  if [ -f "$d/restore.sh" ]; then
    managed_vols+=("$(basename "$d")")
  fi
done

if [ ${#managed_vols[@]} -eq 0 ]; then
  echo "${red}No generated restore scripts found.${reset}"
  echo "Use 'dockvault generate' first to set up scripts."
  exit 1
fi

# Let user pick volume
PS3="${yellow}Select volume to restore: ${reset}"
select vol in "${managed_vols[@]}"; do
  if [[ -n "$vol" ]]; then
    script_path="$DOCKVAULT_HOME/$vol/restore.sh"
    echo "Launching: $script_path"
    echo "------------------------------------------------"
    bash "$script_path"
    break
  else
    echo "${red}Invalid selection.${reset}"
  fi
done < /dev/tty
