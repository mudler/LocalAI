##
## A bash script wrapper that runs the autogptq server with conda

# Activate conda environment
source activate autogptq

# get the directory where the bash script is located
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

python $DIR/autogptq.py