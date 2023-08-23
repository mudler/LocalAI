# Continue

![logo](https://continue.dev/docs/assets/images/continue-cover-logo-aa135cc83fe8a14af480d1633ed74eb5.png)

This document presents an example of integration with [continuedev/continue](https://github.com/continuedev/continue).

![Screenshot](https://continue.dev/docs/assets/images/continue-screenshot-1f36b99467817f755739d7f4c4c08fe3.png)

For a live demonstration, please click on the link below:

- [How it works (Video demonstration)](https://www.youtube.com/watch?v=3Ocrc-WX4iQ)

## Integration Setup Walkthrough

1. [As outlined in `continue`'s documentation](https://continue.dev/docs/getting-started), install the [Visual Studio Code extension from the marketplace](https://marketplace.visualstudio.com/items?itemName=Continue.continue) and open it.
2. In this example, LocalAI will download the gpt4all model and set it up as "gpt-3.5-turbo". Refer to the `docker-compose.yaml` file for details.

    ```bash
    # Clone LocalAI
    git clone https://github.com/go-skynet/LocalAI

    cd LocalAI/examples/continue

    # Start with docker-compose
    docker-compose up --build -d
    ```

3. Type `/config` within Continue's VSCode extension, or edit the file located at `~/.continue/config.py` on your system with the following configuration:

    ```py
    from continuedev.src.continuedev.libs.llm.openai import OpenAI, OpenAIServerInfo

    config = ContinueConfig(
       ...
       models=Models(
            default=OpenAI(
               api_key="my-api-key",
               model="gpt-3.5-turbo",
               openai_server_info=OpenAIServerInfo(
                  api_base="http://localhost:8080",
                  model="gpt-3.5-turbo"
               )
            )
       ),
    )
    ```

This setup enables you to make queries directly to your model running in the Docker container. Note that the `api_key` does not need to be properly set up; it is included here as a placeholder.

If editing the configuration seems confusing, you may copy and paste the provided default `config.py` file over the existing one in `~/.continue/config.py` after initializing the extension in the VSCode IDE.

## Additional Resources

- [Official Continue documentation](https://continue.dev/docs/intro)
- [Documentation page on using self-hosted models](https://continue.dev/docs/customization#self-hosting-an-open-source-model)
- [Official extension link](https://marketplace.visualstudio.com/items?itemName=Continue.continue)
