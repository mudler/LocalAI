## Gets latest GGUF models from HF:
## Example:
## local-ai run hermes-2-theta-llama-3-8b
## OPENAI_BASE_URL="http://192.168.xx.xx:8080" python scripts/latest_hf.py   

import requests
import subprocess
import os
import sys
# get current directory where the script is
current_dir = os.path.dirname(os.path.realpath(__file__))

def get_latest_model():
    search_term = "GGUF"
    if len(sys.argv) > 2 and sys.argv[1]:
        search_term = sys.argv[1]
    url = "https://huggingface.co/api/models"
    params = {"sort": "lastModified", "direction": -1, "limit": 30, "search": search_term}
    response = requests.get(url, params=params)

    if response.status_code == 200:
        models = response.json()
        if models:
            for model in models:
                print(f"Model: {model['modelId']}")
                subprocess.run(["python", current_dir+"/model_gallery_info.py", model['modelId']])
                
        else:
            print("No models found.")
    else:
        print(f"Failed to fetch models. Status code: {response.status_code}")


if __name__ == "__main__":
    get_latest_model()
