// Command apexentries generates gallery entries for the mudler APEX GGUF
// repositories: one parent entry per model carrying a variants list, plus one
// child entry per imatrix tier, per unsloth quant rung, and per speculative
// build.
//
// Builds are discovered by inspecting the filenames a repo actually publishes.
// Repo names do not reliably predict them: mudler/gemma-4-26B-A4B-it-APEX-GGUF
// ships gemma-4-26B-A4B-APEX-*.gguf, and three other repos drop a suffix or a
// vendor prefix in the same way.
package main

func main() {}
