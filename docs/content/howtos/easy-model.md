
+++
disableToc = false
title = "Easy Model Setup"
weight = 2
+++

Lets learn how to setup a model, for this ``How To`` we are going to use the ``Dolphin 2.2.1 Mistral 7B`` model.

To download the model to your models folder, run this command in a commandline of your picking.
```bash
curl --location 'http://localhost:8080/models/apply' \
--header 'Content-Type: application/json' \
--data-raw '{
    "id": "TheBloke/dolphin-2.2.1-mistral-7B-GGUF/dolphin-2.2.1-mistral-7b.Q4_0.gguf"
}'
```

Each model needs at least ``5`` files, with out these files, the model will run raw, what that means is you can not change settings of the model.
```
File 1 - The model's GGUF file
File 2 - The model's .yaml file
File 3 - The Chat API .tmpl file
File 4 - The Chat API helper .tmpl file
File 5 - The Completion API .tmpl file
```
So lets fix that! We are using ``lunademo`` name for this ``How To`` but you can name the files what ever you want! Lets make blank files to start with

```bash
touch lunademo-chat.tmpl
touch lunademo-chat-block.tmpl
touch lunademo-completion.tmpl
touch lunademo.yaml
```
Now lets edit the `"lunademo-chat.tmpl"`, This is the template that model "Chat" trained models use, but changed for LocalAI

```txt
<|im_start|>{{if eq .RoleName "assistant"}}assistant{{else if eq .RoleName "system"}}system{{else if eq .RoleName "user"}}user{{end}}
{{if .Content}}{{.Content}}{{end}}
<|im_end|>
```

For the `"lunademo-chat-block.tmpl"`, Looking at the huggingface repo, this model uses the ``<|im_start|>assistant`` tag for when the AI replys, so lets make sure to add that to this file. Do not add the user as we will be doing that in our yaml file!

```txt
{{.Input}}
<|im_start|>assistant
```

Now in the `"lunademo-completion.tmpl"` file lets add this. (This is a hold over from OpenAI V0)

```txt
{{.Input}}
```


For the `"lunademo.yaml"` file. Lets set it up for your computer or hardware. (If you want to see advanced yaml configs - [Link](https://localai.io/advanced/))

We are going to 1st setup the backend and context size.

```yaml
backend: llama
context_size: 2000
```

What this does is tell ``LocalAI`` how to load the model. Then we are going to **add** our settings in after that. Lets add the models name and the models settings. The models ``name:`` is what you will put into your request when sending a ``OpenAI`` request to ``LocalAI``
```yaml
name: lunademo
parameters:
  model: dolphin-2.2.1-mistral-7b.Q4_0.gguf
```

Now that LocalAI knows what file to load with our request, lets add the template files to our models yaml file now.
```yaml
template:
  chat: lunademo-chat-block
  chat_message: lunademo-chat
  completion: lunademo-completion
```

If you are running on ``GPU`` or want to tune the model, you can add settings like (higher the GPU Layers the more GPU used)
```yaml
f16: true
gpu_layers: 4
```

To fully tune the model to your like. But be warned, you **must** restart ``LocalAI`` after changing a yaml file

```bash
docker compose restart
```

If you want to check your models yaml, here is a full copy!
```yaml
backend: llama
context_size: 2000
##Put settings right here for tunning!! Before name but after Backend!
name: lunademo
parameters:
  model: dolphin-2.2.1-mistral-7b.Q4_0.gguf
template:
  chat: lunademo-chat-block
  chat_message: lunademo-chat
  completion: lunademo-completion
```

Now that we got that setup, lets test it out but sending a [request]({{%relref "easy-request" %}}) to Localai! 

## ----- Adv Stuff -----

**(Please do not run these steps if you have already done the setup)**
Alright now that we have learned how to set up our own models, here is how to use the gallery to do alot of this for us. This command will download and set up (mostly, we will **always** need to edit our yaml file to fit our computer / hardware)
```bash
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{
     "id": "model-gallery@lunademo"
   }'  
```

This will setup the model, models yaml, and both template files (you will see it only did one, as completions is out of date and not supported by ``OpenAI`` if you need one, just follow the steps from before to make one.
If you would like to download a raw model using the gallery api, you can run this command. You will need to set up the 3 files needed to run the model tho!
```bash
curl --location 'http://localhost:8080/models/apply' \
--header 'Content-Type: application/json' \
--data-raw '{
    "id": "NAME_OFF_HUGGINGFACE/REPO_NAME/MODENAME.gguf",
    "name": "REQUSTNAME"
}'
```

