# Skill: Cancel a Distributed Load

1. Read the model's load-status (`GET /api/models/{id}/load-status`) and take the exact `job_id` from it. Never guess a job id.
2. Say what you will cancel and ask for confirmation under safety rule 1.
3. Call `cancel_model_load` with that model and job id, only after the confirmation.
4. Read the state. `stopped` means the worker confirmed the work ended. `stopping` means the cancel is recorded and the stop is pending; the model is free again after `retry_after` seconds regardless. `gone` means no such load exists any more.
5. On a conflict, a different attempt is now current. Read load-status again and ask before cancelling that one. Do not treat a conflict or an unknown model as success.
