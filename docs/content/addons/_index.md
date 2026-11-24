+++
title = "Add-ons"
description = "Install, use and build add-ons."
weight = 4
type = "chapter"
icon = "extension"
devbuild = "addon"

[cascade]
  devbuild = "addon"
+++

An add-on is a separate program that adds tools to LocalAI, such as reading a mailbox, making an office document or running code. An administrator installs it. It runs as the same operating system user as LocalAI, so install only add-ons you trust.

- [User guide]({{% relref "/addons/user-guide" %}}): find, install, turn on, update and remove add-ons.
- [Catalogue]({{% relref "/addons/catalogue" %}}): what each add-on adds and what it needs.
- [Build your own add-on]({{% relref "/addons/build-your-own" %}}): a tutorial with one tool and one skill.
- [Build an add-on in your own repository]({{% relref "/features/addon-sdk" %}}): use the Go SDK packages from another module and pack a zip file with `addonpkg`.
- [Paired clients]({{% relref "/addons/addon-clients" %}}): inference with a client key, and the routes, event stream and host operations of an add-on client.
- [Contract reference]({{% relref "/addons/contract-reference" %}}): the full specification for add-on authors.
- [Testing add-ons]({{% relref "/addons/testing" %}}): how to run focused add-on tests.
