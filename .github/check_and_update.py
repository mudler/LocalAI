import hashlib
from huggingface_hub import hf_hub_download, get_paths_info
import requests
import sys
import os

uri = sys.argv[1]
file_name = uri.split('/')[-1]

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

def manual_safety_check_hf(repo_id):
    scanResponse = requests.get('https://huggingface.co/api/models/' + repo_id + "/scan")
    scan = scanResponse.json()
    if scan['hasUnsafeFile']:
        return scan
    return None

download_type, repo_id_or_url = parse_uri(uri)

new_checksum =  None
file_path = None

# Decide download method based on URI type
if download_type == 'huggingface':
    # Check if the repo is flagged as dangerous by HF
    hazard = manual_safety_check_hf(repo_id_or_url)
    if hazard != None:
        print(f'Error: HuggingFace has detected security problems for {repo_id_or_url}: {str(hazard)}', filename=file_name)
        sys.exit(5)
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
