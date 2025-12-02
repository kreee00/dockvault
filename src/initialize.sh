# Set up workspace
export DOCKVAULT_HOME="${DOCKVAULT_HOME:-$HOME/dockvault_scripts}"
mkdir -p "$DOCKVAULT_HOME"
mkdir -p "$DOCKVAULT_HOME/logs"

# Ensure PATH
export PATH=$PATH:/usr/local/bin:/usr/bin:/bin
