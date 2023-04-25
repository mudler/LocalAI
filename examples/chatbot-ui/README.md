# chatbot-ui


## Setup
- Set the `services > api > volumes` parameter in the docker-compose.yaml file. This should point to your local folder with your models.
- Copy/Rename your model file to `gpt-3.5-turbo` (without any .bin file extension).
- Type `docker compose up` to run the api and the Web UI.
- Open http://localhost:3000 for the Web UI.


## Known issues
- Can't select the model from the UI. Seems hardcoded to `gpt-3.5-turbo`.
- If your machine is slow, the UI will timeout on the request to the API.


### Links

- [mckaywrigley/chatbot-ui](https://github.com/mckaywrigley/chatbot-ui)
