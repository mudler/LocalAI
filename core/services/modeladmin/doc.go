// Package modeladmin owns the operations that mutate or read the
// configuration of an *already-installed* model on disk: full YAML edits
// (with rename), JSON deep-merge patches, enable/disable, pin/unpin, VRAM
// estimation, and read-back of the on-disk YAML.
//
// It exists so the same logic can be called from two places:
//
//   - HTTP handlers in core/http/endpoints/localai/* — the existing REST
//     surface (PUT/PATCH/POST under /models/...).
//   - In-process MCP clients (pkg/mcp/localaitools/inproc) — the LocalAI
//     Assistant chat modality calls these helpers directly so the
//     in-process tool surface and the REST surface stay in sync.
//
// Distinct from core/services/galleryop, which owns *sourcing* models
// (install from a gallery, delete, upgrade). modeladmin only manages
// configs and runtime flags of models that already exist locally.
package modeladmin
