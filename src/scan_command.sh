check_dependencies

echo "${green}== Docker Volume Scan ==${reset}"

# Define format for the table
# %-30s = Left align, 30 chars wide
# %-10s = Left align, 10 chars wide
format="%-35s %-12s %s\n"

printf "$format" "VOLUME NAME" "DRIVER" "USED BY CONTAINER(S)"
printf "$format" "-----------" "------" "--------------------"

# Iterate through all volumes
# We read line by line: volume_name driver
docker volume ls --format '{{.Name}} {{.Driver}}' | while read -r vol driver; do
    
    # Find containers using this volume
    # --filter volume=XYZ finds containers mounting that specific volume
    # paste -sd "," - joins multiple lines with a comma (e.g., "container1,container2")
    containers=$(docker ps -a --filter volume="$vol" --format '{{.Names}}' | paste -sd "," -)
    
    # If no container found, show a dash
    if [ -z "$containers" ]; then
        containers="${yellow}(dangling)${reset}"
    else
        containers="${cyan}${containers}${reset}"
    fi

    printf "$format" "$vol" "$driver" "$containers"
done

echo ""
echo "${blue}Tip: Use 'dockvault generate' to backup one of these volumes.${reset}"
