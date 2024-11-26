## This script simply help pull off some info from the HF api
## to speed up addition of new models to the gallery.
## It accepts as input a repo_id and returns part of the YAML data
## Use it as:
## OPENAI_BASE_URL="<api_url>" OPENAI_MODEL="" python .github/add_model.py mradermacher/HaloMaidRP-v1.33-15B-L3-i1-GGUF
## Example: 
# local-ai run hermes-2-theta-llama-3-8b
# OPENAI_BASE_URL="http://192.168.xx.xx:8080" OPENAI_MODEL="hermes-2-theta-llama-3-8b" python scripts/model_gallery_info.py mradermacher/HaloMaidRP-v1.33-15B-L3-i1-GGUF

import sys
import os
from openai import OpenAI
from huggingface_hub import HfFileSystem, get_paths_info

templated_yaml = """
- !!merge <<: *llama3
  name: "{model_name}"
  urls:
    - https://huggingface.co/{repo_id}
  description: |
    {description}
  overrides:
    parameters:
      model: {file_name}
  files:
    - filename: {file_name}
      sha256: {checksum}
      uri: huggingface://{repo_id}/{file_name}
"""

client = OpenAI()

model = os.environ.get("OPENAI_MODEL", "hermes-2-theta-llama-3-8b")


def summarize(text: str) -> str:
    chat_completion = client.chat.completions.create(
        messages=[
            {
                "role": "user",
                "content": "You are a bot which extracts the description of the LLM model from the following text. Return ONLY the description of the model, and nothing else.\n" + text,
            },
        ],
        model=model,
    )

    return chat_completion.choices[0].message.content

def format_description(description):
    return '\n    '.join(description.split('\n'))

# Example usage
if __name__ == "__main__":
    # Get repoid from argv[0]
    repo_id = sys.argv[1]
    token = ""  # Replace with your Hugging Face token if needed

    fs = HfFileSystem()
    all_files = fs.ls(repo_id, detail=False)

    print(all_files)

    # Find a file that has Q4_K in the name
    file_path = None
    file_name = None
    readmeFile = None
    for file in all_files:
        print(f"File found: {file}")
        if "readme" in file.lower():
            readmeFile = file
            print(f"Found README file: {readmeFile}")
        if "q4_k_m" in file.lower():
            file_path = file

    
    if file_path is None:
        print("No file with Q4_K_M found, using the first file in the list.")
        exit(1)    


    # Extract file from full path (is the last element)
    if file_path is not None:
        file_name = file_path.split("/")[-1]
    

    model_name = repo_id.split("/")[-1]

    checksum = None
    for file in get_paths_info(repo_id, [file_name], repo_type='model'):
        try:
            checksum = file.lfs.sha256
            break
        except Exception as e:
            print(f'Error from Hugging Face Hub: {str(e)}', file=sys.stderr)
            sys.exit(2)

    print(checksum)
    print(file_name)
    print(file_path)

    summarized_readme = ""

    if readmeFile:
        # If there is a README file, read it
        readme = fs.read_text(readmeFile)
        try:
            summarized_readme = summarize(readme)
        except Exception as e:
            print(f"Error summarizing the README: {str(e)}", file=sys.stderr)            
        summarized_readme = format_description(summarized_readme)

    print("Model correctly processed")
    ## Append to the result YAML file
    with open("result.yaml", "a") as f:
        f.write(templated_yaml.format(model_name=model_name.lower().replace("-GGUF","").replace("-gguf",""), repo_id=repo_id, description=summarized_readme, file_name=file_name, checksum=checksum, file_path=file_path))
   