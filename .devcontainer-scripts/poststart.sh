#!/bin/bash

cd /workspace

# Ensures generated source files are present upon load
make prepare

echo "Standard Post-Start script completed."

if [ -f "/devcontainer-customization/poststart.sh" ]; then
    echo "Launching customization poststart.sh"
    bash "/devcontainer-customization/poststart.sh"
fi