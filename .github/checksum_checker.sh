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
    new_checksum=$(python3 -c "
import hashlib
from huggingface_hub import hf_hub_download, get_paths_info
import requests
import sys
import os

uri = '$uri'
file_name = uri.split('/')[-1]

# Function to parse the URI and determine download method
# Function to parse the URI and determine download method
def parse_uri(uri):
    if uri.startswith('huggingface://'):
        repo_id = uri.split('://')[1]
        return 'huggingface', repo_id.rsplit('/', 1)[0]
    elif 'huggingface.co' in uri:
        parts = uri.split('/resolve/')
        if len(parts) > 1:
            repo_path = parts[0].split('https://huggingface.co/')[-1]
            return 'huggingface', repo_path
    return 'direct', uri

def calculate_sha256(file_path):
    sha256_hash = hashlib.sha256()
    with open(file_path, 'rb') as f:
        for byte_block in iter(lambda: f.read(4096), b''):
            sha256_hash.update(byte_block)
    return sha256_hash.hexdigest()

download_type, repo_id_or_url = parse_uri(uri)

new_checksum =  None

# Decide download method based on URI type
if download_type == 'huggingface':
    # Use HF API to pull sha
    for file in get_paths_info(repo_id_or_url, [file_name], repo_type='model'):
        try:
            new_checksum = file.lfs.sha256
            break
        except Exception as e:
            print(f'Error from Hugging Face Hub: {str(e)}', file=sys.stderr)
            sys.exit(2)
    if new_checksum is None:
        try:
            file_path = hf_hub_download(repo_id=repo_id_or_url, filename=file_name)
        except Exception as e:
            print(f'Error from Hugging Face Hub: {str(e)}', file=sys.stderr)
            sys.exit(2)
else:
    response = requests.get(repo_id_or_url)
    if response.status_code == 200:
        with open(file_name, 'wb') as f:
            f.write(response.content)
        file_path = file_name
    elif response.status_code == 404:
        print(f'File not found: {response.status_code}', file=sys.stderr)
        sys.exit(2)
    else:
        print(f'Error downloading file: {response.status_code}', file=sys.stderr)
        sys.exit(1)

if new_checksum is None:
    new_checksum = calculate_sha256(file_path)
    print(new_checksum)
    os.remove(file_path)
else:
    print(new_checksum)

")

    if [[ "$new_checksum" == "" ]]; then
        echo "Error calculating checksum for $file_name. Skipping..."
        return
    fi

    echo "Checksum for $file_name: $new_checksum"

    # Compare and update the YAML file if checksums do not match
    result=$?
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
