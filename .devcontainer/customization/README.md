Place any additional resources your environment requires in this directory

Script hooks are currently called for:
`postcreate.sh` and `poststart.sh`

If files with those names exist here, they will be called at the end of the normal script.

This is a good place to set things like `git config --global user.name` are set - and to handle any other files that are mounted via this directory.

An example of a useful script might be:

```
#!/bin/bash
gcn=$(git config --global user.name)
if [ -z "$gcn" ]; then
    git config --global user.name YOUR.NAME
    git config --global user.email YOUR.EMAIL
    git remote add PREFIX FORK_URL
fi
```