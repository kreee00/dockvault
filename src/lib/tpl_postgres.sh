# $1 = volume_name, $2 = container, $3 = user, $4 = db_name, $5 = password
gen_backup_logic_postgres() {
    local vol="$1"
    local cont="$2"
    local user="$3"
    local db="$4"
    local pass="$5"

    cat <<EOF

ARCHIVE_NAME="${vol}_pgdump_\${TIMESTAMP}.sql.gz"
echo "\${yellow}Dumping PostgreSQL Database...\${reset}"

# We pass PGPASSWORD env var to the container so pg_dump authenticates automatically
docker exec -e PGPASSWORD='$pass' -t $cont pg_dump -U $user $db | gzip > "\$TEMP_DIR/\$ARCHIVE_NAME"
EOF
}

# $1 = container, $2 = user, $3 = db_name, $4 = password
gen_restore_logic_postgres() {
    local cont="$1"
    local user="$2"
    local db="$3"
    local pass="$4"

    cat <<EOF
echo "\${yellow}[INFO] Streaming SQL dump to PostgreSQL...\${reset}"

if [[ "\$FILENAME" == *.gz ]]; then
  zcat "\$LOCAL_FILE" | docker exec -i -e PGPASSWORD='$pass' $cont psql -U $user -d $db
else
  cat "\$LOCAL_FILE" | docker exec -i -e PGPASSWORD='$pass' $cont psql -U $user -d $db
fi
EOF
}