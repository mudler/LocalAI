+++
disableToc = true
title = "Face"
description = "Find out whose face is in a photo, or whether two photos show the same person."
weight = 40
keywords = ["face", "recognition", "photo", "add-on"]
+++

The Face add-on (ID `face`) is the page for face recognition. It is separate from the Voice add-on. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | One page, `/app/addons/face/recognition`: who is this, same person, and the faces you enrolled. The detect and embedding tools are under **More tools**. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature (off by default for new users on a server with accounts). The page also needs the `face_recognition` feature, which is on by default. The routes stay in the core and are checked on every call. |
| Guide | [Face Recognition]({{% relref "/models/capabilities/vision/face-recognition#the-webui-page" %}}) |
