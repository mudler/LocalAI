# rwkv

Example of how to run rwkv models.

## Run models

Setup:

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/rwkv

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# build the tooling image to convert an rwkv model locally:
docker build -t rwkv-converter -f Dockerfile.build .

# download and convert a model (one-off) - it's going to be fast on CPU too!
docker run -ti --name converter -v $PWD:/data rwkv-converter https://huggingface.co/BlinkDL/rwkv-4-raven/resolve/main/RWKV-4-Raven-1B5-v11-Eng99%25-Other1%25-20230425-ctx4096.pth /data/models/rwkv

# Get the tokenizer
wget https://raw.githubusercontent.com/saharNooby/rwkv.cpp/5eb8f09c146ea8124633ab041d9ea0b1f1db4459/rwkv/20B_tokenizer.json -O models/rwkv.tokenizer.json

# start with docker-compose
docker-compose up -d --build
```

Test it out:

```bash
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
    "model": "gpt-3.5-turbo",
    "prompt": "A long time ago, in a galaxy far away",
    "max_tokens": 100,
    "temperature": 0.9, "top_p": 0.8, "top_k": 80
  }'

# {"object":"text_completion","model":"gpt-3.5-turbo","choices":[{"text":", there was a small group of five friends: Annie, Bryan, Charlie, Emily, and Jesse."}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "gpt-3.5-turbo",            
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9, "top_p": 0.8, "top_k": 80
   }'

# {"object":"chat.completion","model":"gpt-3.5-turbo","choices":[{"message":{"role":"assistant","content":" Good, thanks. I am about to go to bed. I' ll talk to you later.Bye."}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}
```

### Fine tuning

See [RWKV-LM](https://github.com/BlinkDL/RWKV-LM#training--fine-tuning). There is also a Google [colab](https://colab.research.google.com/github/resloved/RWKV-notebooks/blob/master/RWKV_v4_RNN_Pile_Fine_Tuning.ipynb).

## See also

- [RWKV-LM](https://github.com/BlinkDL/RWKV-LM)
- [rwkv.cpp](https://github.com/saharNooby/rwkv.cpp)