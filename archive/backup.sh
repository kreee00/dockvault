#!/bin/sh

# --- CONFIGURATION ---
log="/backup/backup.log"

# IMPORTANT: Replace "dockvault_backups" with your actual remote name.
GDRIVE_REMOTE_NAME="dockvault_backups" 

# NOTE: Paths are now separated by a pipe '|'. You can type the full path 
# without worrying about quotes, as the pipe is the delimiter.
# Format: [Container Source Path] | [Local Backup Folder Name] | [Google Drive Target Path]
BACKUP_DATA_SOURCES="
/n8n_data|n8n|Docker Volume Backup/n8n
/postgres_data|postgres|Docker Volume Backup/postgres
"

# --- FUNCTION DEFINITION (No changes needed inside the function) ---

perform_backup() {
    # Arguments are passed as $1, $2, $3 via the 'read' command in the main loop
    local source_path="$1"
    local local_folder_name="$2"
    local gdrive_target_path="$3"
    
    local service_name=`basename "$source_path"`
    
    echo "--- [Starting Backup for: $service_name] ---" | tee -a "$log"

    # 1. Create Backup File in the local target directory
    local local_target_dir="/backup/$local_folder_name"
    local backup_file="${local_target_dir}/${service_name}_backup_`date +%Y%m%d_%H%M%S`.tar.gz"
    
    # Check if source volume is accessible and navigate to it
    cd "$source_path" || { echo "[ERROR] Source volume $source_path not found" | tee -a "$log"; return 1; }
    
    # Create the tarball from the volume content
    tar czf "$backup_file" . || { echo "[ERROR] tar failed for $service_name" | tee -a "$log"; return 1; }
    echo "[Backup completed: $backup_file]" | tee -a "$log"

    # 2. Upload to Google Drive
    local gdrive_destination="${GDRIVE_REMOTE_NAME}:${gdrive_target_path}"
    echo "[Upload started to: $gdrive_destination]" | tee -a "$log"
    
    # Use rclone copy to upload the file to the specific folder path
    rclone copy "$backup_file" "$gdrive_destination" --drive-chunk-size 128M -v 2>&1 | tee -a "$log"
    
    echo "[Upload completed for $service_name]" | tee -a "$log"

    # 3. Local Cleanup (Keep only the latest two local backups of THIS service)
    echo "[Cleanup started for local backups of $service_name in $local_target_dir]" | tee -a "$log"
    
    local to_delete=`ls -tp ${local_target_dir}/${service_name}_backup_*.tar.gz 2>/dev/null | grep -v '/$' | tail -n +3`

    if [ -n "$to_delete" ]; then
        echo "$to_delete" | while read -r file; do
            echo "[Deleting old local backup: $file]" | tee -a "$log"
            rm "$file"
        done
    fi
    
    local retained=`ls -tp ${local_target_dir}/${service_name}_backup_*.tar.gz 2>/dev/null | grep -v '/$' | head -n 2 | tr '\n' ' '`
    echo "[Retained local backups for $service_name: $retained]" | tee -a "$log"
}

# --- MAIN EXECUTION (Updated parsing logic) ---

echo "[Script started at `date +"%Y-%m-%d %H:%M:%S"`]" | tee -a "$log"

# Set Internal Field Separator (IFS) to the pipe symbol '|' for parsing
# The 'local IFS' keeps the change only within the while loop scope.
echo "$BACKUP_DATA_SOURCES" | while IFS="|" read -r source_path local_folder_name gdrive_target_path; do
    
    # Skip empty lines (like the first or last blank line)
    [ -z "$source_path" ] && continue 

    # The three variables are now populated directly by 'read'
    perform_backup "$source_path" "$local_folder_name" "$gdrive_target_path"
done

echo "[All tasks completed at `date +"%Y-%m-%d %H:%M:%S"`]" | tee -a "$log"
