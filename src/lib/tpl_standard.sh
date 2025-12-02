# $1 = volume_name
gen_backup_logic_standard() {
    local vol="$1"
    
    cat <<EOF

ARCHIVE_NAME="${vol}_\${TIMESTAMP}.tar.gz"
echo "Creating archive: \$ARCHIVE_NAME"

echo "\${yellow}Compressing files (Detailed Log):\${reset}"
# Using 'sh -c' to ensure wildcards work inside the container
docker run --rm \\
  -v ${vol}:/source:ro \\
  -v \$TEMP_DIR:/dest \\
  alpine sh -c "tar -czvf /dest/\$ARCHIVE_NAME -C /source ."
EOF
}

# $1 = volume_name
gen_restore_logic_standard() {
    local vol="$1"
    
    cat <<EOF
echo "\${yellow}[INFO] Wiping volume and extracting...\${reset}"
docker run --rm \\
  -v ${vol}:/target \\
  -v \$TEMP_DIR:/source \\
  alpine sh -c "cd /target && rm -rf ./* && tar -xzf /source/\$FILENAME -C /target"
EOF
}
