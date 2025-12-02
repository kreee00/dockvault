#!/bin/bash

check_dependencies

remote="${DOCKVAULT_RCLONE_REMOTE}"
target_path="${args[path]}"
detailed="${args[--detailed]}"

# ==============================================================================
# HELPER: Get Latest File Info
# ==============================================================================
get_latest_file() {
    local full_path="$1"
    # Sort by date/time descending and pick top 1
    local latest=$(rclone lsl "${remote}:${full_path}" --max-depth 1 2>/dev/null | sort -k2,3 -r | head -n 1)
    
    if [ -n "$latest" ]; then
        local size=$(echo "$latest" | awk '{print $1}')
        local date=$(echo "$latest" | awk '{print $2}')
        local time=$(echo "$latest" | awk '{print $3}' | cut -d. -f1)
        local name=$(echo "$latest" | cut -d' ' -f4-)
        
        # Human readable size
        if [ ${#size} -gt 6 ]; then
           size="$((${size}/1024/1024))MB"
        else
           size="$((${size}/1024))KB"
        fi
        
        echo "$name ($date $time | $size)"
    fi
}

# ==============================================================================
# LOGIC: Standard Tree
# ==============================================================================
print_tree_standard() {
    local remote="$1"
    local current_path="$2"
    local indent="$3"
    local is_last="$4"
    
    local display_name=$(basename "$current_path")
    if [ -z "$display_name" ]; then
        display_name="/"
    fi
    
    if [ "$is_last" = "true" ]; then
        echo "${indent}└── 📁 $display_name"
        indent="${indent}    "
    else
        echo "${indent}├── 📁 $display_name"
        indent="${indent}│   "
    fi
    
    local folders=()
    while IFS= read -r folder; do
        folders+=("$folder")
    done < <(rclone lsf "${remote}:${current_path}" --dirs-only --max-depth 1 2>/dev/null | grep -v '^$')
    
    local count=${#folders[@]}
    local i=0
    for folder in "${folders[@]}"; do
        i=$((i + 1))
        local is_last_child="false"
        if [ $i -eq $count ]; then
            is_last_child="true"
        fi
        
        folder=$(echo "$folder" | sed 's|/$||')
        print_tree_standard "$remote" "${current_path%/}/${folder}" "$indent" "$is_last_child"
    done
}

# ==============================================================================
# LOGIC: Detailed Tree (FIXED VERSION)
# ==============================================================================
print_tree_detailed() {
    local remote="$1"
    local current_path="$2"
    local indent="$3"
    local is_last="$4"
    local depth="$5"
    
    local display_name=$(basename "$current_path")
    if [ -z "$display_name" ]; then
        display_name="/"
    fi
    
    # Print folder with appropriate tree characters
    if [ "$is_last" = "true" ]; then
        echo "${indent}└── 📁 ${cyan}$display_name${reset}"
        child_indent="${indent}    "
    else
        echo "${indent}├── 📁 ${cyan}$display_name${reset}"
        child_indent="${indent}│   "
    fi
    
    # Get subfolders
    local folders=()
    while IFS= read -r folder; do
        folders+=("$folder")
    done < <(rclone lsf "${remote}:${current_path}" --dirs-only --max-depth 1 2>/dev/null | grep -v '^$')
    
    local count=${#folders[@]}
    
    # If no subfolders, check for files and show latest
    if [ $count -eq 0 ]; then
        local latest_file_info=$(get_latest_file "$current_path")
        if [ -n "$latest_file_info" ]; then
            # Check if there are any files at all
            local file_count=$(rclone lsf "${remote}:${current_path}" --max-depth 1 2>/dev/null | grep -v '/$' | wc -l)
            if [ $file_count -gt 0 ]; then
                # Show latest file with proper tree character
                if [ "$is_last" = "true" ]; then
                    echo "${child_indent}└── ${green}📄 Latest: $latest_file_info${reset}"
                else
                    echo "${child_indent}├── ${green}📄 Latest: $latest_file_info${reset}"
                fi
                
                # If there are more than 1 files, show count
                if [ $file_count -gt 1 ]; then
                    if [ "$is_last" = "true" ]; then
                        echo "${child_indent}    └── ${yellow}... and $((file_count - 1)) more file(s)${reset}"
                    else
                        echo "${child_indent}│   └── ${yellow}... and $((file_count - 1)) more file(s)${reset}"
                    fi
                fi
            else
                # No files found, show empty
                if [ "$is_last" = "true" ]; then
                    echo "${child_indent}└── ${yellow}(empty)${reset}"
                else
                    echo "${child_indent}├── ${yellow}(empty)${reset}"
                fi
            fi
        fi
    else
        # Process subfolders recursively
        local i=0
        for folder in "${folders[@]}"; do
            i=$((i + 1))
            local is_last_child="false"
            if [ $i -eq $count ]; then
                is_last_child="true"
            fi
            
            folder=$(echo "$folder" | sed 's|/$||')
            print_tree_detailed "$remote" "${current_path%/}/${folder}" "$child_indent" "$is_last_child" "$((depth + 1))"
        done
    fi
}

# ==============================================================================
# EXECUTION ENTRY POINT
# ==============================================================================

# Start the tree from target_path
if [ -z "$target_path" ]; then
    target_path=""
fi

# Clean target path - remove trailing slashes
target_path=$(echo "$target_path" | sed 's|/*$||')

if [[ "$detailed" == "1" ]]; then
    echo "${green}== Detailed Remote View ($remote) ==${reset}"
    echo "Fetching details (this may take a moment)..."
    echo ""
    
    # Print the remote root
    if [ -z "$target_path" ]; then
        echo "📁 ${remote}:/"
    else
        echo "📁 ${remote}:${target_path}"
    fi
    
    # Get immediate children of the root
    folders=()
    while IFS= read -r folder; do
        folders+=("$folder")
    done < <(rclone lsf "${remote}:${target_path}" --dirs-only --max-depth 1 2>/dev/null | grep -v '^$')
    
    # Process each folder under root
    count=${#folders[@]}
    i=0
    for folder in "${folders[@]}"; do
        i=$((i + 1))
        is_last="false"
        if [ $i -eq $count ]; then
            is_last="true"
        fi
        
        folder=$(echo "$folder" | sed 's|/$||')
        print_tree_detailed "$remote" "${target_path%/}/${folder}" "" "$is_last" "1"
    done
    
    # Handle case where target path has no subfolders (it's a leaf directory)
    if [ ${#folders[@]} -eq 0 ]; then
        latest_file_info=$(get_latest_file "$target_path")
        if [ -n "$latest_file_info" ]; then
            local file_count=$(rclone lsf "${remote}:${target_path}" --max-depth 1 2>/dev/null | grep -v '/$' | wc -l)
            if [ $file_count -gt 0 ]; then
                echo "└── ${green}📄 Latest: $latest_file_info${reset}"
                if [ $file_count -gt 1 ]; then
                    echo "    └── ${yellow}... and $((file_count - 1)) more file(s)${reset}"
                fi
            fi
        fi
    fi
    
else
    echo "${green}== Remote Tree View ($remote) ==${reset}"
    echo "Fetching structure..."
    echo ""
    
    # Print the remote root
    if [ -z "$target_path" ]; then
        echo "📁 ${remote}:/"
    else
        echo "📁 ${remote}:${target_path}"
    fi
    
    # Get immediate children of the root
    folders=()
    while IFS= read -r folder; do
        folders+=("$folder")
    done < <(rclone lsf "${remote}:${target_path}" --dirs-only --max-depth 1 2>/dev/null | grep -v '^$')
    
    # Process each folder under root
    count=${#folders[@]}
    i=0
    for folder in "${folders[@]}"; do
        i=$((i + 1))
        is_last="false"
        if [ $i -eq $count ]; then
            is_last="true"
        fi
        
        folder=$(echo "$folder" | sed 's|/$||')
        print_tree_standard "$remote" "${target_path%/}/${folder}" "" "$is_last"
    done
    
    echo ""
    echo "${blue}Tip: Use 'dockvault tree --detailed' to see backup file info.${reset}"
fi

echo ""
