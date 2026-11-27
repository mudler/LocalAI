---
description: Find a model in the LocalAI gallery and install it.
argument-hint: <model name>
---

Install the model "$ARGUMENTS" on the LocalAI server. Follow the skill `localai:models`. Use the API at `$LOCALAI` (default `http://localhost:8080`), with an administrator API key when the server has keys.

1. If "$ARGUMENTS" is empty, ask the user which model to install and stop.
2. List the installed models (`GET /v1/models`). If "$ARGUMENTS" is there, say it is installed already and stop.
3. Search the gallery (`GET /models/available`) for names that contain "$ARGUMENTS", ignoring case.
   - No match: say so, show up to five close names, and stop.
   - One exact match: use it.
   - Several matches and no exact one: show up to five, with their size, and ask the user to pick one. Stop until they answer.
4. Install it: `POST /models/apply` with `{"id": "<gallery>@<name>"}`.
5. Follow the job at `GET /models/jobs/<uuid>`. Check every few seconds until `processed` is true. Report the progress in one short line, not at every check.
6. When it ends:
   - No `error`: say the model is installed and give its name for the `model` field.
   - `error` set: quote the error in plain words. Do not retry.

Install one model only. Do not remove or change other models.
