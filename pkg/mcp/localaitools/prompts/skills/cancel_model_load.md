# Skill: Cancel a Distributed Load

1. Obtain the exact model and job_id from GET /api/models/{id}/load-status. Never guess a generation.
2. Explain that uncertain remote work remains quarantined. Ask for confirmation under safety rule 1.
3. Call `cancel_model_load` with that model and job_id only after confirmation.
4. Report uncertain as pending. On conflict, re-read status and ask again rather than canceling a replacement. Unknown is not success.
