# $1 = volume_name, $2 = container, $3 = user, $4 = db_name, $5 = password
gen_backup_logic_mysql() {
    local vol="$1"
    local cont="$2"
    local user="$3"
    local db="$4"
    local pass="$5"

    cat <<EOF

ARCHIVE_NAME="${vol}_mysqldump_\${TIMESTAMP}.sql.gz"
echo "\${yellow}Dumping MySQL Database...\${reset}"

# We pass MYSQL_PWD env var to the container so mysqldump can authenticate
docker exec -e MYSQL_PWD='$pass' $cont mysqldump -u $user $db | gzip > "\$TEMP_DIR/\$ARCHIVE_NAME"
EOF
}

# $1 = container, $2 = user, $3 = db_name, $4 = password
gen_restore_logic_mysql() {
    local cont="$1"
    local user="$2"
    local db="$3"
    local pass="$4"

    cat <<EOF
echo "\${yellow}[INFO] Streaming SQL dump to MySQL...\${reset}"

# Using env var for password to avoid interactive prompt
if [[ "\$FILENAME" == *.gz ]]; then
  zcat "\$LOCAL_FILE" | docker exec -i -e MYSQL_PWD='$pass' $cont mysql -u $user $db
else
  cat "\$LOCAL_FILE" | docker exec -i -e MYSQL_PWD='$pass' $cont mysql -u $user $db
fi
EOF
}