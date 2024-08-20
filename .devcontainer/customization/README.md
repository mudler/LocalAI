Place any additional resources your environment requires in this directory

Script hooks are currently called for:
`postcreate.sh` and `poststart.sh`

If files with those names exist here, they will be called at the end of the normal script.

This is a good place to set things like `git config --global user.name` are set - and to handle any other files that are mounted via this directory.

To assist in doing so, `source /.devcontainer-scripts/utils.sh` will provide utility functions that may be useful - for example:

```
#!/bin/bash

source "/.devcontainer-scripts/utils.sh"

sshfiles=("config", "key.pub")

setup_ssh "${sshfiles[@]}"

config_user "YOUR NAME" "YOUR EMAIL"

config_remote "REMOTE NAME" "REMOTE URL"

```