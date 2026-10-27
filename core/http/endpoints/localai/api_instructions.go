package localai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/swagger"
	"github.com/mudler/xlog"
)

const swaggerDefsPrefix = "#/definitions/"

// instructionDef is a lightweight instruction definition that maps to swagger tags.
type instructionDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Intro       string   `json:"-"` // brief context not in swagger
}

var instructionDefs = []instructionDef{
	{Name: "motion", Description: "Live human pose capture with Protobuf over WebSocket.", Tags: []string{"motion"}, Intro: "Create /api/motion/sessions and negotiate localai.motion.v2 on /poses for binary Protobuf RGB uploads and pose outputs with JSON flow credits. Browsers can POST /api/motion/sessions/{id}/tickets for a single-use 30-second WebSocket ticket. No SONIC controller or offline export."},
	{Name: "chats", Description: "Chats the server keeps: steering, sub-agents and live events", Tags: []string{"chats"}, Intro: "POST /api/chats starts a chat with a model that cannot change later. POST .../{id}/messages sends a message: with no turn running it starts one; with a turn running it waits (started false, pending_id) and reaches the model, together with any others and in order, at the model's next request. GET .../{id} lists the waiting messages; PATCH and DELETE .../{id}/pending/{pid} edit or cancel one until it is delivered (409 after). After a stop or a failed turn the waiting messages stay; POST .../{id}/send-now starts a turn from them and POST .../{id}/stop ends a running turn. GET .../{id}/messages reads the transcript and GET .../{id}/events streams live server-sent events, so read the transcript first. A conversation belongs to its user; another user's id is always 404. POST /api/chats/import takes the chats the old chat page kept in the browser (the value of localStorage localai_chats_data) and stores them as yours; a chat that is there already is skipped. Needs the chats feature, which is on by default. Your files: GET /api/chats/files?path= lists a folder of your workspace (paths are inside your home, \"/\"), GET and PUT /api/chats/files/content?path= download and upload a file (a second upload that does not name the version it started from keeps both files and reports conflict_of), POST .../files/folders and .../files/move create and rename, DELETE .../files?path= moves to the Trash, GET .../files/trash lists deletions and POST .../files/undo {rev} restores one, GET .../files/history lists changes and GET .../files/search?q= searches names and text. POST .../{id}/messages also takes multipart/form-data (text and files) to attach files when no turn runs. Skills: GET /api/chats/skills?chat= lists the skills of the four layers (folder, personal, shared, builtin) with enabled and shadowed, POST creates one {layer personal|shared, name, description, body} (shared needs an administrator), GET PUT DELETE .../skills/{layer}/{name} read, change and delete (to the Trash), PUT /api/chats/{id}/skills {off} switches skills off for one chat. Add-ons: GET /api/chats/{id}/addons lists the add-ons whose tools you may use, each with its switch for that chat, and PUT /api/chats/{id}/addons {off: [ids]} switches the tools of add-ons off for one chat. Saved agents: GET and POST /api/chats/agents, GET PUT DELETE .../agents/{id}: an agent is {name, description, instructions, model, mcp_servers, addons, collections, skills}; the lists are exact grants (an empty list grants nothing) and a chat gets only what you may use at that moment. POST /api/chats {agent_id} starts a chat with one (the agent's model replaces model when it has one). Inbox: GET /api/chats/notifications?unread=1 lists the notifications of your trigger runs (kind run_ended, run_failed or needs_attention) with the unread count, GET .../notifications/count counts them, POST .../notifications/read {ids} or {all: true} marks them read, DELETE .../notifications {ids} removes them, and GET .../notifications/events streams new ones as server-sent events. Opening a chat (GET /api/chats/{id}) marks its notifications read. Triggers: GET and POST /api/chats/triggers, GET PUT DELETE .../triggers/{id}, POST .../{id}/run (start now), .../{id}/enable and .../{id}/disable, GET .../{id}/runs, GET .../triggers/preview?when=&tz= (next times of a schedule). A trigger is {name, prompt, kind schedule|file, schedule + time_zone, or folder + quiet_seconds, daily_cap, enabled, agent_id, model, source_chat_id}: it starts a chat as you by itself, on a schedule (\"daily 08:00\", \"weekdays 09:30\", \"weekly mon,thu 09:30\", \"monthly 15 08:00\", \"once 2026-12-24 18:00\" or five cron fields) or when a file arrives in the folder; a chat it starts has origin trigger. A model can propose a trigger in a chat (propose_trigger); GET .../triggers/proposals lists them and POST .../triggers/proposals/{id}/confirm {time_zone} turns one into a trigger that is on, DELETE dismisses it. Browser push: GET /api/chats/push/key returns the server's VAPID public key, POST /api/chats/push/subscribe takes a browser PushSubscription {endpoint, keys: {p256dh, auth}} (an https endpoint on a public address) and DELETE .../push/subscribe {endpoint} removes it; a notification is then pushed with its title and link only. These answer 409 push_off when the server keeps nothing across a restart. Conversion: at start the server converts the old chat page, agents, tasks, skills and Telegram connectors; GET /api/chats/conversion lists what happened to your items (kind, name, status converted|skipped|failed|dropped, reason) and gives an administrator the totals. Converted triggers and connectors are switched off. Connectors: GET and POST /api/chats/connectors, GET PUT DELETE .../connectors/{id}, POST .../{id}/enable, .../disable and .../test (asks Telegram who the bot is), GET .../{id}/senders (who wrote to the bot) and POST .../{id}/senders/{sender}/allow. A binding is {name, token (write-only, sealed, never returned; has_token says it is stored), agent_id (required: an agent of yours that names a model), enabled, accept_any, allow [chat IDs, user IDs or @usernames], notify_chat}: a person who messages the Telegram bot talks to that agent in a conversation of their own, as you, with only the agent's grants. The allowlist is empty at first, so nobody is accepted; a sender who is not accepted is ignored and logged. accept_any lets anyone who can message the bot use the agent. Files and skills answer 409 with the reason when the server has no data path. No MCP tool maps to these routes: they are the user's own chat and files, not administration. GET POST /api/chats/mcp-connectors, GET PUT DELETE /api/chats/mcp-connectors/{id} and POST .../enable, .../disable, .../test manage your MCP connectors: MCP servers you define (transport http, or stdio for administrators outside distributed mode), stored per user, with token, headers and env sealed and never returned; a test answers 200 with ok and the tool names or the error. Addresses on private networks are refused at connect time unless an administrator sets mcp_connectors_allow_private. A chat with no agent gets your enabled connectors; a saved agent names one as connector:<id> in mcp_servers. GET /api/chats/{id}/tools lists the add-ons, model MCP servers and connectors of a chat with their switches; PUT /api/chats/{id}/mcp {off: [server name or connector:<id>]} switches MCP servers and connectors off for that chat. MCP Apps: a tool that declares an interactive page (_meta.ui.resourceUri) gives its tool_end event and its transcript line an app {server, tool, resource_uri, visibility}; the transcript line also holds the call input and result. GET /api/chats/{id}/mcp-apps/resource?server=&uri= reads the ui:// page (html, mime_type, meta with csp, permissions and prefersBorder) for a server the chat may use now and a resource a tool of that server declares, and POST /api/chats/{id}/mcp-apps/call {server, tool, arguments} runs a tool of the same server whose visibility includes app, for the page to call; both answer 403 mcp_app_refused for anything else and need the MCP feature."},
	{Name: "addons", Description: "Owner-routed addon lifecycle and response relays", Tags: []string{"Addons"}, Intro: "POST /api/addons/dispatch submits typed lifecycle operations or reads actor-bound operation-status. POST /api/addons/relay serves status and coarse lifecycle progress on the receiving frontend. Named authentication and the addons feature are required; lifecycle operations require administration. Preserve operationId after outcome_unknown and poll status, never submit a new mutation. An add-on that refuses a request as invalid answers 400 invalid_request from every route, never an outage: nothing was applied, so correct the request instead of repeating it. Resource and output routes deny access until host admission and resolution providers are installed. Data and schema mutations are stored proposals: POST /api/addons/proposals previews and stores one (nothing is applied), GET /api/addons/proposals/{id} reads it, GET /api/addons/proposals?state=pending lists the caller's own pending proposals (newest first, at most 32; where a proposal made through the OpenAI-compatible route waits for the proposer's own browser session; a chat tool applies its own at once), POST .../cancel withdraws one and POST .../reconcile resolves an unknown outcome by receipt lookup, and when no receipt can be found POST .../resolve lets the resource owner's own browser session abandon it with a reason (same confirmation-surface rules as confirm; nothing is applied again). The owner role finds what to resolve with GET /api/addons/resources/{resourceId}/proposals?state=unresolved (same rules, X-LocalAI-Confirm equals the resource ID): it lists the proposal ID, proposer, effects, digest and age of every unresolved proposal on the resource, never its arguments. A write blocked by an unresolved outcome is denied (403) and, for the owner role only, names it as outcome_blocked with blockedBy. POST .../confirm applies exactly the stored operation once and is accepted only from the proposer's own browser session (same-origin fetch, X-LocalAI-Confirm header equal to the proposal ID, digest of what was shown); API keys, MCP sessions and tool or agent calls can propose but never confirm, and no MCP tool maps to confirm. Sharing and database deletion are never proposals. A read that returns a file result gives private artifact IDs, never addon handles: GET /api/addons/artifacts/{id} returns the transfer state, name, size and expiry, and GET /api/addons/artifacts/{id}/download streams the complete file as an attachment. Only the requesting principal can read or download an artifact, an unknown, foreign or expired ID is the same 403, access to every source resource is checked again at completion and while streaming, and a disabled addon denies downloads until it is enabled again. Only GET /backends/library/updates has an MCP tool, list_backend_library_updates; no MCP tool maps to the other routes. The Apps surface: GET /api/addons/apps lists the installed, enabled addons any caller with the addons feature may use, with title, description, icon URL, version, generation, state (running, starting, failed or stopped; an observation separate from enabled), a main page path only when the manifest declares a valid one, and a bounded tool summary; administrators also see disabled addons and per-app controls. GET /api/addons/apps/{id}/icon?generation=N serves the verified icon of a visible app. POST /api/addons/apps/{id}/start and .../stop are administrator-only, take {operationId, generation} with the generation that was listed, deny a stale generation and keep the lifecycle rules (preserve operationId after outcome_unknown). No MCP tool maps to the Apps routes. The listed ui entry of an app gives the same-origin URLs of its UI code: GET /api/addons/{id}/assets/{digest}/{path} streams one declared file (the entry module or a style sheet) of the current installed package, relayed from the owner, with Cache-Control private, max-age=31536000, immutable and nosniff. The digest is the package sha256 in lower-case hex; a file the manifest does not declare, a traversal, another addon, an older package and a disabled or uninstalled addon are all one 403, never cached. A credential in the URL is never accepted; the browser uses its same-origin session. No MCP tool maps to the asset route. Management (administrators only, through POST /api/addons/dispatch): the reads catalog-search, catalog-details, catalog-sources and addons-installed return catalog entries, one resolved entry, the configured libraries with their last fetch state and the installed addons with verified package facts, health, retained generations and storage; catalog-source-save stores or removes a library by reference only (a secret in place of a reference is denied) and catalog-source-refresh fetches one again; upgrade installs a newer release of an installed addon. Removing a library never touches installed addons. The installed view has an update object computed by the owner's update check (state, candidate version and digest, check and last-success time, reasons, redacted error class); an add-on the owner cannot answer for has none, and a failed check is never up-to-date. No MCP tool maps to these actions. UI-only resource management (never tools; no MCP tool maps to any of it): GET /api/addons/resources?addonId=tables lists the resources the caller owns or that were shared with the caller, with the caller's own role, the display name, the owner's display name for a shared one and, for the owner role only, the number of people it is shared with and the revision. POST /api/addons/resources creates one by the person's own action (operationId makes it idempotent; the arguments are the add-on's create tool arguments), POST .../{resourceId}/rename and .../delete rename and delete it (the owner role only; generation and revision must be the listed ones or the call is 409 stale; delete needs confirm equal to the display name, removes the data and every grant and never uninstalls the add-on), GET .../{resourceId}/grants, PUT/DELETE .../grants/{userId} and GET .../{resourceId}/users?q= list, set (viewer or editor, whole resource) and remove grants and search the people it can be shared with (immutable account ID and display name only; local-owner mode answers 409 sharing_disabled). All of them accept only the owner's own browser session with the X-LocalAI-Confirm header equal to the resource ID (the operation ID for a creation); API keys are refused. POST .../{resourceId}/apply changes the data or the schema by the person's own action with no card, through a write or schema tool the add-on declares ({operationId, generation, revision, tool, arguments} with the revision the caller read; editors write rows, only the owner changes the schema; 409 stale when the generation or revision moved; the same operationId with the same request is one change; reads, creations, rename, delete and undeclared tools are refused). POST .../resources/{resourceId}/changes/{changeId}/undo ({operationId, generation}) undoes one change an add-on tool made at once from a chat, an agent or a trigger, by the change set the add-on kept for it; the change ID is the one the tool result named (undoable true, changeId, undo). It is the person's own action in a browser tab and accepts only the browser session with X-LocalAI-Confirm equal to the resource ID: an API key, an MCP session or a tool call is refused, so a model can never undo work. The role is the one the change needed (editors undo data changes, only the owner role undoes a schema change or a creation). It is applied against the revision the add-on just reported: when the resource changed after the change the answer is 409 stale with the words of what changed and nothing is overwritten. The other refusals are typed: not_found (404), expired (410, after 7 days), already_undone (409) and cannot_undo (409, for example a job that finished); the same operationId with the same change is one undo. GET .../resources/{resourceId}/operations/{operationId}?kind=rename|delete|apply|undo reads the receipt of a rename, a deletion, a change or an undo after outcome_unknown, which carries the original operationId. GET/PUT/DELETE /api/addons/conversations/{conversationId}/selection/{addonId} keep the resource a conversation works on for the authenticated user; a chat tool call that names no resourceId uses it, authorized again by the owner on every call, and with none selected the tool answers an error result with code select_resource. Data recovery (administrators only, never tools; no MCP tool maps to any of it): GET /api/addons/{id}/recovery returns the recovery state of an add-on's data (ready, upgrading, restoring, needs-recovery or purging), its backups newest first with size, reason, mode and last verification result, the retention policy and the receipt of the last restore. POST /api/addons/{id}/recovery/backup makes a verified, consistent backup (the add-on is stopped while its SQLite files are copied with VACUUM INTO, admission is blocked and pending proposals are invalidated; nothing is left behind on failure; 503 insufficient_storage when it does not fit), .../verify reads one completely, .../restore restores one (the add-on must not be running, confirm is the add-on ID, mapping names the account that takes the data of a backup made in local-owner mode, the answer carries warnings and no share is given back that was removed since the backup or whose account is gone; 409 needs_recovery, addon_running, backup_corrupt, incompatible_format, identity_changed, mapping_required or downgrade_unsupported), .../delete-backup removes one (never the only verified copy, 409 sole_recovery_copy) and .../prune applies the retention policy. POST /api/addons/{id}/purge removes retained data by explicit scope (data, backups, recoveryCopies) with confirm equal to the add-on ID, for an add-on that is uninstalled or disabled; it is not a database deletion or an uninstall. Every mutation accepts only the administrator's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the operationId); a repeated operationId returns the recorded result and never runs again; after outcome_unknown read the state and keep the operationId. An upgrade of an add-on that has data is backed up, migrated under exclusive ownership and published only when healthy; a failed migration that changes the data format leaves the add-on in needs-recovery with the previous package and the verified backup retained. Updates (never tools; no MCP tool maps to any of it): GET /api/addons/{id}/update reads the owner's update status of an installed add-on for every caller with the addons feature who may see it (administrators any installed add-on, others an enabled one; the same 403 otherwise). The state is unknown, up-to-date, update-available, incompatible (reasons: platform, host-version, api-version, protocol-version, sdk-version, data-format), check-failed (a redacted error class and lastSuccess, the time the library was last read; never shown as up-to-date) or integrity-error (an immutable version was listed again with another digest; never an update). A moving channel (development, a branch, latest) compares resolved digests, so a changed digest with the same version label is an update; an immutable version selects the highest compatible greater version of the same major. The library and the channel the add-on came from never change by themselves. Callers who are not administrators get the state, versions and times only. The route never uses the network. POST /api/addons/{id}/update/check (administrators, own browser session, X-LocalAI-Confirm equal to the operationId) fetches the library again and returns the status; it is rate limited and answers throttled true when it did not fetch, and it applies nothing. POST /api/addons/{id}/upgrade (administrators, own browser session) is the only request that applies an update: it carries operationId, expectedDigest (the installed package the caller saw) and candidateDigest (the candidate the check resolved), and the owner re-resolves, re-checks compatibility and refuses before it records anything with 409 stale_installed, candidate_changed, incompatible, integrity_error or upgrade_conflict (the same transition under another operationId); 503 check_failed when the library cannot be read. A failure keeps the last good generation or enters the recovery state; the previous installation is never deleted. The apps list marks an app update: update-pending from the owner's cached check only and never reaches a library."},
	{Name: "library-sources", Description: "Library sources of add-ons, backends and models", Tags: []string{"library"}, Intro: "Administrators only. GET /api/library/sources lists every source of the add-on, backend and model kinds, shipped defaults and removed defaults (tombstones) included, with kind, id, name, url, mirrors, enabled, default, removed, suspended, a conflict message and the last fetch state. POST /api/library/sources adds a source of your own ({kind: addon|backend|model, url, id, name, format: library|gallery}); the same kind and URL twice is a 409. POST /api/library/sources/{id}/enable and /disable turn one on or off (enabling a removed default brings it back). PUT /api/library/sources/{id} replaces the mirrors of a model or backend source ({mirrors: [..]}); POST also takes mirrors. Each mirror is an http(s), github:, oci:// or file:// address, at most 8. DELETE /api/library/sources/{id} removes a source; a shipped default stays as a tombstone so it does not return at the next start. The routes work on the store that `local-ai library` edits. On a frontend (--addons-role=frontend) the routes forward to the add-on owner, which is the only writer; they answer 503 when the owner is not reachable or runs on another platform. `GET /backends/library/updates` and `POST /backends/library/install` forward the same way. The runtime setting official_libraries (default on) turns every shipped default off at once: they stay listed with suspended true. No MCP tool maps to these routes."},
	{Name: "addon-navigation", Description: "The built-in tiles and the per-user pins of the Add-ons drawer", Tags: []string{"AddonNavigation"}, Intro: "GET /api/addons/builtin lists the page add-ons the caller may open. GET and PUT /api/addons/pins read and replace the caller's ordered pins. All of them need an authenticated caller; none needs the addons feature or the administrator role."},
	{Name: "fine-tuning-quantization", Description: "Export status of the fine-tuning and quantization add-ons", Tags: []string{"fine-tuning", "quantization"}, Intro: "GET /api/fine-tuning/status and GET /api/quantization/status return {\"model_exports\": bool}. It is false only when this instance does not see the models path of the add-on owner, so exporting or importing a model here would write where the owner cannot read; it is the same value as the model_exports key of GET /api/features. The routes need an authenticated caller with the fine-tuning or quantization permission and answer 503 when add-ons are off. No MCP tool maps to them: the value is a read-only switch for the add-on UIs."},
	{
		Name:        "chat-inference",
		Description: "OpenAI-compatible chat completions, text completions, and embeddings",
		Tags:        []string{"inference", "embeddings"},
		Intro:       "Set \"stream\": true for SSE streaming. Supports tool/function calling when the model config has function templates configured.",
	},
	{
		Name:        "moderation",
		Description: "OpenAI-compatible text moderation using a local completion model",
		Tags:        []string{"moderation"},
		Intro:       "POST /v1/moderations accepts a text string or array plus a LocalAI completion model. LocalAI constrains the model to the OpenAI moderation category schema and returns one result per input. Multimodal moderation inputs are not yet supported.",
	},
	{
		Name:        "audio",
		Description: "Text-to-speech, voice activity detection, transcription, speaker diarization, sound classification, and sound generation",
		Tags:        []string{"audio"},
		Intro:       "GET /v1/audio/voices lists named voices for installed TTS models and accepts an optional model filter. Diarization (/v1/audio/diarization) returns speaker-labelled time segments. Backends with native ASR-diarization (vibevoice-cpp) can also emit per-segment text via include_text=true; backends with a dedicated pipeline (sherpa-onnx + pyannote) emit segmentation only. include_sounds=true adds timed sound events (start, end, label, confidence) when the model has a sound companion, otherwise 501 include_sounds_unsupported. Response formats: json (default), verbose_json (adds speakers summary + text), rttm (NIST format). Sound classification (/v1/audio/classification) returns scored AudioSet sound-event tags (audio tagging via the ced backend); top_k and threshold control the returned set.",
	},
	{
		Name:        "voice-library",
		Description: "Create, preview, list, and delete reusable voice-cloning reference profiles",
		Tags:        []string{"voice-profiles"},
		Intro:       "Profiles persist below the LocalAI data directory as private PCM-WAV references plus exact transcripts. GET and audio preview require the audio_speech feature; create and delete are admin-only. Pass the returned `voice` URI to /v1/audio/speech or /tts. LocalAI resolves the URI at request time and never exposes a filesystem path.",
	},
	{
		Name:        "images",
		Description: "Image generation and inpainting",
		Tags:        []string{"images"},
	},
	{
		Name:        "model-management",
		Intro:       "GET /api/models/{id}/load-status reports job_id and the lease of a distributed load. Admin POST /api/models/{id}/load-cancel needs that job_id: 200 means stopped or gone, 202 means the cancel is recorded and the stop is pending (retry_after says when the model is released regardless), 409 means a different attempt is current. Never cancel a replacement attempt.",
		Description: "Browse the gallery, install, delete, and manage models and backends",
		Tags:        []string{"models", "backends"},
	},
	{
		Name:        "config-management",
		Description: "Discover, read, and modify model configuration fields with VRAM estimation",
		Tags:        []string{"config"},
		Intro:       "Fields with static options include an \"options\" array in metadata. Fields with dynamic values have an \"autocomplete_provider\" for runtime lookup.",
	},
	{
		Name:        "monitoring",
		Description: "System metrics, backend status, API and backend traces, backend process logs, and system information",
		Tags:        []string{"monitoring"},
		Intro:       "Includes real-time backend log streaming via WebSocket at /ws/backend-logs/:modelId.",
	},
	{
		Name:        "mcp",
		Description: "Model Context Protocol — tool-augmented chat with MCP servers",
		Tags:        []string{"mcp"},
		Intro:       "The model's config must define MCP servers. The endpoint handles tool execution automatically.",
	},
	{
		Name:        "video",
		Description: "Video generation from text prompts with optional image or audio conditioning",
		Tags:        []string{"video"},
		Intro:       "POST /video accepts start_image, end_image, and audio as public URL, base64, or data URI. Backend-specific tuning is passed as string values in params.",
	},
	{
		Name:        "3d",
		Description: "3D mesh generation, remeshing, and animation (binary glTF / GLB)",
		Tags:        []string{"3d"},
		Intro:       "POST /3d/generations accepts a conditioning image as public URL, base64, or data URI (no text prompt) and returns one .glb asset as a URL under /generated-3d or as b64_json. quality selects the mesh pipeline (auto|coarse|512|1024); background controls solid-background removal (auto|keep|black|white); step, texture_steps, and cfg_scale tune the flow sampling. POST /3d/remesh accepts multipart model, mesh (GLB), and a single detail percentage to return a watertight print-ready GLB; the enclosing offset is derived automatically. POST /3d/animate accepts named inputs with type and data, plus string-valued params. Consult the selected model's three_d_operations from /v1/models/capabilities for its required inputs, parameters, and output type. Kimodo produces text-conditioned animated skeletons without a mesh or skin; animation inputs are not universally text-only.",
	},
	{
		Name:        "forecast",
		Description: "Time-series forecasting: quantile trajectories for numeric series, with optional covariates",
		Tags:        []string{"forecast"},
		Intro:       "POST /v1/forecast accepts { model, prediction_length, series: [{ id, target, past_covariates, known_covariates, past_categorical, known_categorical }], quantiles, max_context }. target is [variates][steps], or a flat array for one variate; null marks a missing value. known_* rows hold steps + prediction_length values. Series of different shapes may share one request: the response keeps the request order. quantiles defaults to 0.1, 0.5 and 0.9 and must be levels the model was trained on. Forecast models serve this route only; they do not appear as chat models. CPU only.",
	},
	{
		Name:        "face-recognition",
		Description: "Face verification (1:1), identification (1:N), embedding, and demographic analysis",
		Tags:        []string{"face-recognition"},
		Intro:       "The /v1/face/register, /identify, and /forget endpoints build on a vector store — registrations are in-memory by default and lost on restart. Use /v1/face/embed for a raw embedding; /v1/embeddings is OpenAI-compatible and text-only.",
	},
	{
		Name:        "voice-recognition",
		Description: "Speaker verification (1:1), embedding, and demographic analysis from voice",
		Tags:        []string{"voice-recognition"},
		Intro:       "Voice (speaker) recognition — the audio analog to /v1/face/*. Use /v1/voice/verify for 1:1 speaker comparison, /v1/voice/identify for 1:N match against the registered store, /v1/voice/{register,forget} to manage that store, /v1/voice/embed for a raw speaker-encoder vector, and /v1/voice/analyze for age / gender / emotion inferred from speech. Registrations are in-memory by default and lost on restart. Audio inputs accept URL, base64, or data-URI; /v1/embeddings remains text-only.",
	},
	{
		Name:        "decisions",
		Description: "Typed local decisions through OpenAI-compatible /v1/decisions and SystemOne APIs",
		Tags:        []string{"systemone"},
		Intro:       "POST /v1/decisions accepts { model, input, questions: [{ type: predicate|choice|score, name, instructions, choices, levels }] } and returns ordered answers with typed string/boolean choices. It shares SystemOne execution and backend limits. POST /v1/systemone accepts { model, state, questions: { <id>: { type: choice|noul|score, instructions, criteria } } }. Both answer all questions in one pass. /v1/systemone/permute re-runs one choice question under n_perm option orders; /v1/systemone/separate answers each question in its own pass. Decision models declare known_usecases: [decisions]; /permute and /separate return 400 for these models. Zero-shot NER models declare token_classify and serve all four routes through the NER path. A vllm-cpp config with no declared usecases is treated as a decision model. Confidence comes from the local pipeline, without an OpenAI calibration guarantee. Usage reflects backend counts; unavailable detail breakdowns are empty objects. /v1/decisions accepts user messages with input_text and inline PNG/JPEG input_image parts; only default auto detail is supported. Text and images are flattened into SystemOne state and an image list. External URLs are never fetched. safety_identifier is compatibility metadata, not a safety service or authentication identity. Text-only bodies are limited to 64 KiB, image-bearing bodies to 16 MiB, and requests to 64 questions. Shared image limits and backend capability checks apply; see the Decisions API docs.",
	},
	{
		Name:        "branding",
		Description: "Whitelabel the instance: configure name, tagline, logo, and favicon",
		Tags:        []string{"branding"},
		Intro:       "GET /api/branding is public so the login screen can render the configured logo before authentication. Text fields are saved through POST /api/settings; binary assets (logo, horizontal logo, favicon) use multipart upload at /api/branding/asset/{kind} and are served back from /branding/asset/{kind}.",
	},
	{
		Name:        "usage-and-billing",
		Description: "Per-user token usage and request counts, with optional cost tracking",
		Tags:        []string{"usage"},
		Intro:       "GET /api/usage returns the current user's token usage in time-bucketed form (day/week/month/all). In single-user no-auth mode the records are attributed to a synthetic local user with stable UUID, so this endpoint and the dashboard work without --auth. /api/usage/all is the cluster-wide view and requires admin (the local user is admin in single-user mode). UsageRecord fields include RequestedModel/ServedModel and PreFilter/PostFilterPromptTokens for routing- and PII-aware accounting.",
	},
	{
		Name:        "pii-filtering",
		Description: "Inspect the NER-based PII filter applied to chat requests",
		Tags:        []string{"pii"},
		Intro:       "PII redaction is NER-based and request-side. A consuming model opts in with `pii: { enabled: true, detectors: [<model>] }` where each detector is a token-classification (token_classify) model. The detection policy lives on the detector model itself in a `pii_detection:` block: `{ min_score, default_action (mask|block|allow), entity_actions: { GROUP: action } }`. Multiple detectors union their hits; overlapping spans resolve to the strongest action (block > mask > allow). PII defaults OFF for non-proxy backends and ON for proxy-* (cloud passthroughs). Besides the inline path, two synchronous service endpoints expose the same engine without an inference request: POST /api/pii/analyze returns the detected entity spans (entity_type, source ner|pattern, start/end, score, action) without mutating the text, and POST /api/pii/redact applies the policy — returning redacted_text, or 400 (type pii_blocked) with the offending entities when a block action fires. Both take `{ text, detectors:[<model>...] }` (or `model` to inherit a consuming model's detectors), require the pii_filter feature (any authenticated user), and record audit events with an `origin` of pii_analyze / pii_redact. GET /api/pii/events returns recent redaction events filtered by correlation_id / user_id / pattern_id / origin (middleware|proxy|pii_analyze|pii_redact); events carry `<source>:<GROUP>` ids — e.g. `ner:EMAIL` for the neural detector, `pattern:ANTHROPIC_KEY` for the regex pattern tier — and an 8-char hash prefix, never the matched value (admin or local-user only). The legacy regex pattern tier and its endpoints (/api/pii/patterns, /test, /decide) were removed.",
	},
	{
		Name:        "middleware-admin",
		Description: "Inspect and configure the routing-module middleware (PII filter and routing)",
		Tags:        []string{"middleware", "pii", "router"},
		Intro:       "GET /api/middleware/status is the single round-trip the /app/middleware admin page reads to render the current state: every model's resolved PII enabled state and the NER detector models it references, recent event count, and the active routing models with their classifier configurations. Admin-only (the synthetic local user is admin in no-auth mode). PII detection policy is edited on each detector model's `pii_detection:` block via the model-config tools/UI — there is no global pattern set to mutate. GET /api/router/decisions returns the routing decision log filtered by correlation_id / user_id / router_model. The same surface is exposed as MCP tools (`get_middleware_status`, `get_pii_events`, `get_router_decisions`) for agent-driven inspection.",
	},
	{
		Name:        "failover",
		Description: "Model failover chains: target health, pinning and switch events",
		Tags:        []string{"failover"},
		Intro:       "A failover chain is a model config with a failover block. Requests for the chain name are served by its highest-priority healthy target; the X-LocalAI-Served-Model response header names it. Subscribe to GET /api/failover/events (SSE) to follow switches.",
	},
	{
		Name:        "intelligent-routing",
		Description: "Per-model `router:` configuration that classifies requests and rewrites the served model",
		Tags:        []string{"router"},
		Intro:       "Add a `router:` block to a ModelConfig to turn it into a routing model. The block declares a classifier (`score` — a small model ranks each policy label, Arch-Router-style; `colbert` — a reranker scores policy descriptions against the prompt; `knn` — similarity-weighted vote over a curated corpus of labelled example prompts), `policies` (the label vocabulary), `candidates` (downstream model + labels it serves; first candidate whose labels cover the active set wins, so order small → large), and a `fallback`. The knn classifier needs a `knn: { embedding_model }` block instead of a classifier_model, and reads a persisted corpus seeded via POST /api/router/{name}/corpus with `{entries: [{text, labels}]}` (admin-only; texts are embedded server-side, persisted under the state dir, and NEVER returned by any endpoint — GET /api/router/{name}/corpus/stats reports label counts only, DELETE /api/router/{name}/corpus wipes it). knn routes to the fallback whenever the prompt is less similar than knn.similarity_threshold to every corpus entry — out-of-corpus prompts are treated as undecidable rather than guessed. When a client addresses the routing model, the RouteModel middleware invokes the classifier, picks a candidate, and rewrites input.Model — the standard model-resolution path then runs ACL, disabled-state, and per-model PII against the chosen target. Depth-1 invariant: candidates must NOT themselves carry a `router:` block; runtime check returns 500 on violation. Decisions are logged to GET /api/router/decisions and surfaced in the /app/middleware Routing tab. POST /api/router/decide is the programmatic decision-oracle: external routers (e.g. an organisation-wide router service) send `{router, input}` and receive the classifier's label set + candidate model WITHOUT LocalAI rewriting, forwarding, or recording the call. Shares the classifier cache with the in-band path so warm-up costs are paid once.",
	},
}

// swaggerState holds parsed swagger spec data, initialised once.
type swaggerState struct {
	once  sync.Once
	spec  map[string]any // full parsed swagger JSON
	ready bool
}

var swState swaggerState

func (s *swaggerState) init() {
	s.once.Do(func() {
		var spec map[string]any
		if err := json.Unmarshal(swagger.SwaggerJSON, &spec); err != nil {
			xlog.Error("failed to parse embedded swagger spec", "err", err)
			return
		}
		s.spec = spec
		s.ready = true
	})
}

// filterSwaggerByTags returns a swagger fragment containing only paths whose
// operations carry at least one of the given tags, plus the definitions they
// reference.
func filterSwaggerByTags(spec map[string]any, tags []string) map[string]any {
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	paths, _ := spec["paths"].(map[string]any)
	allDefs, _ := spec["definitions"].(map[string]any)

	filteredPaths := make(map[string]any)
	for path, methods := range paths {
		methodMap, ok := methods.(map[string]any)
		if !ok {
			continue
		}
		filteredMethods := make(map[string]any)
		for method, opRaw := range methodMap {
			op, ok := opRaw.(map[string]any)
			if !ok {
				continue
			}
			opTags, _ := op["tags"].([]any)
			for _, t := range opTags {
				if ts, ok := t.(string); ok && tagSet[ts] {
					filteredMethods[method] = op
					break
				}
			}
		}
		if len(filteredMethods) > 0 {
			filteredPaths[path] = filteredMethods
		}
	}

	// Collect all $ref definitions used by the filtered paths.
	neededDefs := make(map[string]bool)
	collectRefs(filteredPaths, neededDefs)

	// Resolve nested refs from definitions themselves.
	changed := true
	for changed {
		changed = false
		for name := range neededDefs {
			if def, ok := allDefs[name]; ok {
				before := len(neededDefs)
				collectRefs(def, neededDefs)
				if len(neededDefs) > before {
					changed = true
				}
			}
		}
	}

	filteredDefs := make(map[string]any)
	for name := range neededDefs {
		if def, ok := allDefs[name]; ok {
			filteredDefs[name] = def
		}
	}

	result := map[string]any{
		"paths": filteredPaths,
	}
	if len(filteredDefs) > 0 {
		result["definitions"] = filteredDefs
	}
	return result
}

// collectRefs walks a JSON structure and collects all $ref definition names.
func collectRefs(v any, refs map[string]bool) {
	switch val := v.(type) {
	case map[string]any:
		if ref, ok := val["$ref"].(string); ok {
			if strings.HasPrefix(ref, swaggerDefsPrefix) {
				refs[ref[len(swaggerDefsPrefix):]] = true
			}
		}
		for _, child := range val {
			collectRefs(child, refs)
		}
	case []any:
		for _, child := range val {
			collectRefs(child, refs)
		}
	}
}

// swaggerToMarkdown renders a filtered swagger fragment into concise markdown.
func swaggerToMarkdown(skillName, intro string, fragment map[string]any) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(skillName)
	b.WriteString("\n")
	if intro != "" {
		b.WriteString("\n")
		b.WriteString(intro)
		b.WriteString("\n")
	}

	paths, _ := fragment["paths"].(map[string]any)
	defs, _ := fragment["definitions"].(map[string]any)

	// Sort paths for stable output.
	sortedPaths := make([]string, 0, len(paths))
	for p := range paths {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, path := range sortedPaths {
		methods, ok := paths[path].(map[string]any)
		if !ok {
			continue
		}
		sortedMethods := sortMethods(methods)
		for _, method := range sortedMethods {
			op, ok := methods[method].(map[string]any)
			if !ok {
				continue
			}
			summary, _ := op["summary"].(string)
			b.WriteString(fmt.Sprintf("\n## %s %s\n", strings.ToUpper(method), path))
			if summary != "" {
				b.WriteString(summary)
				b.WriteString("\n")
			}

			// Parameters
			params, _ := op["parameters"].([]any)
			bodyParams, nonBodyParams := splitParams(params)

			if len(nonBodyParams) > 0 {
				b.WriteString("\n**Parameters:**\n")
				b.WriteString("| Name | In | Type | Required | Description |\n")
				b.WriteString("|------|----|------|----------|-------------|\n")
				for _, p := range nonBodyParams {
					pm, ok := p.(map[string]any)
					if !ok {
						continue
					}
					name, _ := pm["name"].(string)
					in, _ := pm["in"].(string)
					typ, _ := pm["type"].(string)
					req, _ := pm["required"].(bool)
					desc, _ := pm["description"].(string)
					b.WriteString(fmt.Sprintf("| %s | %s | %s | %v | %s |\n", name, in, typ, req, desc))
				}
			}

			if len(bodyParams) > 0 {
				for _, p := range bodyParams {
					pm, ok := p.(map[string]any)
					if !ok {
						continue
					}
					schema, _ := pm["schema"].(map[string]any)
					refName := resolveRefName(schema)
					if refName != "" {
						b.WriteString(fmt.Sprintf("\n**Request body** (`%s`):\n", refName))
						renderSchemaFields(&b, refName, defs)
					}
				}
			}

			// Responses
			responses, _ := op["responses"].(map[string]any)
			if len(responses) > 0 {
				sortedCodes := make([]string, 0, len(responses))
				for code := range responses {
					sortedCodes = append(sortedCodes, code)
				}
				sort.Strings(sortedCodes)
				for _, code := range sortedCodes {
					resp, ok := responses[code].(map[string]any)
					if !ok {
						continue
					}
					desc, _ := resp["description"].(string)
					respSchema, _ := resp["schema"].(map[string]any)
					refName := resolveRefName(respSchema)
					if refName != "" {
						b.WriteString(fmt.Sprintf("\n**Response %s** (`%s`): %s\n", code, refName, desc))
						renderSchemaFields(&b, refName, defs)
					} else if desc != "" {
						b.WriteString(fmt.Sprintf("\n**Response %s**: %s\n", code, desc))
					}
				}
			}
		}
	}

	return b.String()
}

// sortMethods returns HTTP methods in a conventional order.
func sortMethods(methods map[string]any) []string {
	order := map[string]int{"get": 0, "post": 1, "put": 2, "patch": 3, "delete": 4}
	keys := make([]string, 0, len(methods))
	for k := range methods {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		oi, oki := order[keys[i]]
		oj, okj := order[keys[j]]
		if !oki {
			oi = 99
		}
		if !okj {
			oj = 99
		}
		return oi < oj
	})
	return keys
}

// splitParams separates body parameters from non-body parameters.
func splitParams(params []any) (body, nonBody []any) {
	for _, p := range params {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if in, _ := pm["in"].(string); in == "body" {
			body = append(body, p)
		} else {
			nonBody = append(nonBody, p)
		}
	}
	return
}

// resolveRefName extracts the definition name from a $ref or returns "".
func resolveRefName(schema map[string]any) string {
	if schema == nil {
		return ""
	}
	if ref, ok := schema["$ref"].(string); ok {
		if strings.HasPrefix(ref, swaggerDefsPrefix) {
			return ref[len(swaggerDefsPrefix):]
		}
	}
	return ""
}

// renderSchemaFields writes a markdown field table for a definition.
func renderSchemaFields(b *strings.Builder, defName string, defs map[string]any) {
	if defs == nil {
		return
	}
	def, ok := defs[defName].(map[string]any)
	if !ok {
		return
	}
	props, ok := def["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return
	}

	// Sort fields
	fields := make([]string, 0, len(props))
	for f := range props {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	b.WriteString("| Field | Type | Description |\n")
	b.WriteString("|-------|------|-------------|\n")
	for _, field := range fields {
		prop, ok := props[field].(map[string]any)
		if !ok {
			continue
		}
		typ := schemaTypeString(prop)
		desc, _ := prop["description"].(string)
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", field, typ, desc))
	}
}

// schemaTypeString returns a human-readable type string for a schema property.
func schemaTypeString(prop map[string]any) string {
	if ref := resolveRefName(prop); ref != "" {
		return ref
	}
	typ, _ := prop["type"].(string)
	if typ == "array" {
		items, _ := prop["items"].(map[string]any)
		if items != nil {
			if ref := resolveRefName(items); ref != "" {
				return "[]" + ref
			}
			it, _ := items["type"].(string)
			if it != "" {
				return "[]" + it
			}
		}
		return "[]any"
	}
	if typ != "" {
		return typ
	}
	return "object"
}

// APIInstructionResponse is the JSON response for a single instruction (?format=json).
type APIInstructionResponse struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Tags            []string       `json:"tags"`
	SwaggerFragment map[string]any `json:"swagger_fragment,omitempty"`
}

// ListAPIInstructionsEndpoint returns all instructions (compact list without guides).
// @Summary List available API instruction areas
// @Description Returns a compact list of instruction areas with descriptions and URLs for detailed guides
// @Tags instructions
// @Produce json
// @Success 200 {object} map[string]any "instructions list with hint"
// @Router /api/instructions [get]
func ListAPIInstructionsEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		type compactInstruction struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
			URL         string   `json:"url"`
		}
		instructions := make([]compactInstruction, len(instructionDefs))
		for i, s := range instructionDefs {
			instructions[i] = compactInstruction{
				Name:        s.Name,
				Description: s.Description,
				Tags:        s.Tags,
				URL:         "/api/instructions/" + s.Name,
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"instructions": instructions,
			"hint":         "Fetch GET {url} for a markdown API guide. Add ?format=json for a raw OpenAPI fragment.",
		})
	}
}

// GetAPIInstructionEndpoint returns a single instruction by name.
// @Summary Get an instruction's API guide or OpenAPI fragment
// @Description Returns a markdown guide (default) or filtered OpenAPI fragment (format=json) for a named instruction
// @Tags instructions
// @Produce json
// @Produce text/markdown
// @Param name path string true "Instruction name (e.g. chat-inference, config-management)"
// @Param format query string false "Response format: json for OpenAPI fragment, omit for markdown"
// @Success 200 {object} APIInstructionResponse "instruction documentation"
// @Failure 404 {object} map[string]string "instruction not found"
// @Router /api/instructions/{name} [get]
func GetAPIInstructionEndpoint() echo.HandlerFunc {
	byName := make(map[string]*instructionDef, len(instructionDefs))
	for i := range instructionDefs {
		byName[instructionDefs[i].Name] = &instructionDefs[i]
	}

	return func(c echo.Context) error {
		name := c.Param("name")
		inst, ok := byName[name]
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "instruction not found: " + name})
		}

		swState.init()
		if !swState.ready {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "swagger spec not available"})
		}

		fragment := filterSwaggerByTags(swState.spec, inst.Tags)

		format := c.QueryParam("format")
		if format == "json" {
			return c.JSON(http.StatusOK, APIInstructionResponse{
				Name:            inst.Name,
				Description:     inst.Description,
				Tags:            inst.Tags,
				SwaggerFragment: fragment,
			})
		}

		guide := swaggerToMarkdown(inst.Name, inst.Intro, fragment)
		return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(guide))
	}
}
