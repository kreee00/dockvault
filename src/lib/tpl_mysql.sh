# $1 = volume_name, $2 = container, $3 = user, $4 = db_name
gen_backup_logic_mysql() {
    local vol="$1"
    local cont="$2"
    local user="$3"
    local db="$4"

    cat <<EOF

ARCHIVE_NAME="${vol}_mysqldump_\${TIMESTAMP}.sql.gz"
echo "\${yellow}Dumping MySQL Database...\${reset}"

docker exec $cont mysqldump -u $user -p\${MYSQL_PWD} $db | gzip > "\$TEMP_DIR/\$ARCHIVE_NAME"
EOF
}

# $1 = container, $2 = user, $3 = db_name
gen_restore_logic_mysql() {
    local cont="$1"
    local user="$2"
    local db="$3"

    cat <<EOF
echo "\${yellow}[INFO] Streaming SQL dump to MySQL...\${reset}"
if [[ "\$FILENAME" == *.gz ]]; then
  zcat "\$LOCAL_FILE" | docker exec -i $cont mysql -u $user $db
else
  cat "\$LOCAL_FILE" | docker exec -i $cont mysql -u $user $db
fi
EOF
}
