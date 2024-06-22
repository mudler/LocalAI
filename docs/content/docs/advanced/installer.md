
+++
disableToc = false
title = "Installer options"
weight = 24
+++

An installation script is available for quick and hassle-free installations, streamlining the setup process for new users.

Can be used with the following command:
```bash
curl https://localai.io/install.sh | sh
```

Installation can be configured with Environment variables, for example: 

```bash
curl https://localai.io/install.sh | VAR=value sh
```

List of the Environment Variables:
| Environment Variable | Description                                                  |
|----------------------|--------------------------------------------------------------|
| **DOCKER_INSTALL**       | Set to "true" to enable the installation of Docker images.    |
| **USE_AIO**              | Set to "true" to use the all-in-one LocalAI Docker image.    |
| **API_KEY**              | Specify an API key for accessing LocalAI, if required.       |
| **CORE_IMAGES**          | Set to "true" to download core LocalAI images.                |
| **PORT**                 | Specifies the port on which LocalAI will run (default is 8080). |
| **THREADS**              | Number of processor threads the application should use. Defaults to the number of logical cores minus one. |
| **VERSION**              | Specifies the version of LocalAI to install. Defaults to the latest available version. |
| **MODELS_PATH**          | Directory path where LocalAI models are stored (default is /usr/share/local-ai/models). |

We are looking into improving the installer, and as this is a first iteration any feedback is welcome! Open up an [issue](https://github.com/mudler/LocalAI/issues/new/choose) if something doesn't work for you!