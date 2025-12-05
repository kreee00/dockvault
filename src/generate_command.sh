check_dependencies

echo "${green}== Script Generation Wizard ==${reset}"

# 1. Inputs
selected_vol=$(select_volume)
echo "Selected Volume: ${green}$selected_vol${reset}"

echo ""
echo "Select Backup Template:"
options=("Standard (Files)" "PostgreSQL (pg_dump)" "MySQL (mysqldump)")
template_type=""
PS3="${yellow}Choose type: ${reset}"
select opt in "${options[@]}"; do
  case $opt in
    "Standard (Files)") template_type="standard"; break ;;
    "PostgreSQL (pg_dump)") template_type="postgres"; break ;;
    "MySQL (mysqldump)") template_type="mysql"; break ;;
    *) echo "${red}Invalid option${reset}" ;;
  esac
done

# DB Inputs
db_user=""
db_pass=""
db_name=""
db_container=""

if [[ "$template_type" != "standard" ]]; then
  echo ""
  echo "${blue}Database Configuration:${reset}"
  echo "Select the running container:"
  db_container=$(select_container)
  
  read -p "Enter Database User: " db_user
  # -s hides the input for security
  read -s -p "Enter Database Password: " db_pass
  echo "" # Newline after silent input
  read -p "Enter Database Name: " db_name
fi

# DETERMINE JOB ID / FOLDER NAME
job_name="$selected_vol"
if [[ -n "$db_name" ]]; then
    safe_db_name=$(echo "$db_name" | tr -dc '[:alnum:]\-\_')
    job_name="${selected_vol}-${safe_db_name}"
fi

echo ""
read -p "Enter Google Drive Upload Path (e.g. backups/production): " gdrive_path
remote="${DOCKVAULT_RCLONE_REMOTE}"

# 2. Paths
vol_dir="${DOCKVAULT_HOME}/${job_name}"
backup_script="${vol_dir}/backup.sh"
restore_script="${vol_dir}/restore.sh"

if [[ -d "$vol_dir" ]] && { [[ -f "$backup_script" ]] || [[ -f "$restore_script" ]]; }; then
  echo ""
  echo "${red}WARNING: Scripts for job '$job_name' already exist!${reset}"
  read -p "${yellow}Do you want to OVERWRITE them? (y/N): ${reset}" choice
  case "$choice" in 
    y|Y ) echo "Overwriting...";;
    * ) echo "Cancelled."; exit 0;;
  esac
fi

if [[ "${args[--dry-run]}" != "1" ]]; then
  mkdir -p "$vol_dir"
fi

# ======================================================
# GENERATE CONTENT
# ======================================================

# Start with Header
b_content=$(tpl_header "$job_name" "$template_type")

# Append Specific Logic based on type
if [[ "$template_type" == "standard" ]]; then
    b_content+=$(gen_backup_logic_standard "$selected_vol")
    r_logic=$(gen_restore_logic_standard "$selected_vol")
    
elif [[ "$template_type" == "postgres" ]]; then
    # Pass db_pass to the function
    b_content+=$(gen_backup_logic_postgres "$job_name" "$db_container" "$db_user" "$db_name" "$db_pass")
    r_logic=$(gen_restore_logic_postgres "$db_container" "$db_user" "$db_name" "$db_pass")
    
elif [[ "$template_type" == "mysql" ]]; then
    # Pass db_pass to the function
    b_content+=$(gen_backup_logic_mysql "$job_name" "$db_container" "$db_user" "$db_name" "$db_pass")
    r_logic=$(gen_restore_logic_mysql "$db_container" "$db_user" "$db_name" "$db_pass")
fi

# Append Footer
b_content+=$(tpl_backup_footer "$remote" "$gdrive_path")

# Append Retention Policy
b_content+=$(tpl_retention_logic "$remote" "$gdrive_path" "30d" "30")

# Generate Full Restore Script
r_content=$(tpl_restore_script "$job_name" "$remote" "$gdrive_path" "$r_logic")

# ======================================================
# WRITE OR PREVIEW
# ======================================================
if [[ "${args[--dry-run]}" == "1" ]]; then
  echo "${blue}--- PREVIEW: backup.sh ---${reset}"
  echo "$b_content"
  echo ""
  echo "${blue}--- PREVIEW: restore.sh ---${reset}"
  echo "$r_content"
else
  # Write
  echo "$b_content" > "$backup_script"
  chmod +x "$backup_script"
  
  echo "$r_content" > "$restore_script"
  chmod +x "$restore_script"
  
  echo "${green}Generated scripts in: $vol_dir${reset}"
  update_master_script "$backup_script"
fi