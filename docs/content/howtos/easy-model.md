
+++
disableToc = false
title = "Easy Model Setup"
weight = 2
+++

Lets Learn how to setup a model, for this ``How To`` we are going to use the ``Luna-Ai`` model (Yes I know haha - ``Luna Midori`` making a how to using the ``luna-ai-llama2`` model - lol)

To download the model to your models folder, run this command in a commandline of your picking.
```bash
curl --location 'http://localhost:8080/models/apply' \
--header 'Content-Type: application/json' \
--data-raw '{
    "id": "TheBloke/Luna-AI-Llama2-Uncensored-GGUF/luna-ai-llama2-uncensored.Q4_K_M.gguf"
}'
```

Each model needs at least ``4`` files, with out these files, the model will run raw, what that means is you can not change settings of the model.
```
File 1 - The model's GGUF file
File 2 - The model's .yaml file
File 3 - The Chat API .tmpl file
File 4 - The Completion API .tmpl file
```
So lets fix that! We are using ``lunademo`` name for this ``How To`` but you can name the files what ever you want! Lets make blank files to start with

```bash
touch lunademo-chat.tmpl
touch lunademo-completion.tmpl
touch lunademo.yaml
```
Now lets edit the `"lunademo-chat.tmpl"`, Looking at the huggingface repo, this model uses the ``ASSISTANT:`` tag for when the AI replys, so lets make sure to add that to this file. Do not add the user as we will be doing that in our yaml file!

```txt
{{.Input}}

ASSISTANT:
```

Now in the `"lunademo-completion.tmpl"` file lets add this.

```txt
Complete the following sentence: {{.Input}}
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
  model: luna-ai-llama2-uncensored.Q4_K_M.gguf
```

Now that we have the model set up, there a few things we should add to the yaml file to make it run better, for this model it uses the following roles.
```yaml
roles:
  assistant: 'ASSISTANT:'
  system: 'SYSTEM:'
  user: 'USER:'
```

What that did is made sure that ``LocalAI`` added the test to the users in the request, so if a message is from ``system`` it shows up in the template as ``SYSTEM:``, speaking of template files, lets add those to our models yaml file now.
```yaml
template:
  chat: lunademo-chat
  completion: lunademo-completion
```

If you are running on ``GPU`` or want to tune the model, you can add settings like
```yaml
f16: true
gpu_layers: 4
```

To fully tune the model to your like. But be warned, you **must** restart ``LocalAI`` after changing a yaml file

```bash
docker-compose restart ##windows
docker compose restart ##linux / mac
```

If you want to check your models yaml, here is a full copy!
```yaml
backend: llama
context_size: 2000
##Put settings right here for tunning!! Before name but after Backend!
name: lunademo
parameters:
  model: luna-ai-llama2-uncensored.Q4_K_M.gguf
roles:
  assistant: 'ASSISTANT:'
  system: 'SYSTEM:'
  user: 'USER:'
template:
  chat: lunademo-chat
  completion: lunademo-completion
```

Now that we got that setup, lets test it out but sending a [request]({{%relref "easy-request" %}}) to Localai! 

## Adv Stuff
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

