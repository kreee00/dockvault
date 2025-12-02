# Visualizes the local workspace hierarchy exactly as requested

echo "${green}== Managed Scripts Hierarchy ==${reset}"
echo "${cyan}$DOCKVAULT_HOME/${reset}            <-- Root Workspace"

# 1. Show Master Script
if [ -f "$DOCKVAULT_HOME/master_backup.sh" ]; then
    echo "├── ${green}master_backup.sh${reset}                <-- The SINGLE script added to Crontab"
else
    echo "├── ${red}master_backup.sh${reset}                <-- (Missing) Run generate to create"
fi

# 2. Show Logs Folder
if [ -d "$DOCKVAULT_HOME/logs" ]; then
    echo "├── ${blue}logs/${reset}                           <-- Centralized logs for all backups"
else
    echo "├── ${red}logs/${reset}                           <-- (Missing)"
fi

# 3. Iterate Volume Folders
# We capture folders into an array to handle the "last element" logic if desired,
# but for simplicity and consistency with the requested format, we use standard tree branches.

found_volumes=0
for d in "$DOCKVAULT_HOME"/*/; do
    dirname=$(basename "$d")
    
    # Skip logs folder as we already showed it
    if [ "$dirname" == "logs" ]; then
        continue
    fi

    if [ -d "$d" ]; then
        found_volumes=1
        echo "└── ${cyan}$dirname/${reset}                      <-- Dedicated folder for volume: $dirname"
        
        # Check Backup Script
        if [ -f "$d/backup.sh" ]; then
            echo "    ├── ${green}backup.sh${reset}                 <-- Specific backup logic"
        else
            echo "    ├── ${red}backup.sh${reset}                 <-- (Missing)"
        fi

        # Check Restore Script
        if [ -f "$d/restore.sh" ]; then
            echo "    └── ${green}restore.sh${reset}                <-- Specific restore logic"
        else
            echo "    └── ${red}restore.sh${reset}                <-- (Missing)"
        fi
    fi
done

if [ $found_volumes -eq 0 ]; then
    echo "└── (No volume folders found)"
fi

echo ""
