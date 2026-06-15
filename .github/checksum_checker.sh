#!/bin/bash
# This scripts needs yq and huggingface_hub to be installed
# to install hugingface_hub run pip install huggingface_hub

# Path to the input YAML file
input_yaml=$1

# Function to download file and check checksum using Python
function check_and_update_checksum() {
    model_name="$1"
    file_name="$2"
    uri="$3"
    old_checksum="$4"
    idx="$5"

    # Download the file and calculate new checksum using Python
    new_checksum=$(python3 ./.github/check_and_update.py $uri)
    result=$?

    if [[ $result -eq 5 ]]; then
        echo "Contaminated entry detected, deleting entry for $model_name..."
        yq eval -i "del([$idx])" "$input_yaml"
        return
    fi

    if [[ "$new_checksum" == "" ]]; then
        echo "Error calculating checksum for $file_name. Skipping..."
        return
    fi

    echo "Checksum for $file_name: $new_checksum"

    # Compare and update the YAML file if checksums do not match
    
    if [[ $result -eq 2 ]]; then
        echo "File not found, deleting entry for $file_name..."
        # yq eval -i "del(.[$idx].files[] | select(.filename == \"$file_name\"))" "$input_yaml"
    elif [[ "$old_checksum" != "$new_checksum" ]]; then
        echo "Checksum mismatch for $file_name. Updating..."
        yq eval -i "del(.[$idx].files[] | select(.filename == \"$file_name\").sha256)" "$input_yaml"
        yq eval -i "(.[$idx].files[] | select(.filename == \"$file_name\")).sha256 = \"$new_checksum\"" "$input_yaml"
    elif [[ $result -ne 0 ]]; then
        echo "Error downloading file $file_name. Skipping..."
    else
        echo "Checksum match for $file_name. No update needed."
    fi
}

# Read the YAML and process each file
len=$(yq eval '. | length' "$input_yaml")
for ((i=0; i<$len; i++))
do
    name=$(yq eval ".[$i].name" "$input_yaml")
    files_len=$(yq eval ".[$i].files | length" "$input_yaml")
    for ((j=0; j<$files_len; j++))
    do
        filename=$(yq eval ".[$i].files[$j].filename" "$input_yaml")
        uri=$(yq eval ".[$i].files[$j].uri" "$input_yaml")
        checksum=$(yq eval ".[$i].files[$j].sha256" "$input_yaml")
        echo "Checking model $name, file $filename. URI = $uri, Checksum = $checksum"
        check_and_update_checksum "$name" "$filename" "$uri" "$checksum" "$i"
    done
done
