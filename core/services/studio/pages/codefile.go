package pages

import "github.com/mudler/LocalAI/core/services/studio/workspace"

// IsCodeFile says whether a path written by a page names code or a document
// instead of data: a file with a code extension, a file named index or
// page.json. The page bridge refuses these writes; the person and the
// assistant write the same files through the Files page and the file tools.
//
// It judges the final segment only, and judges what the store and the page
// loader would see: the path is percent-decoded (up to three layers), taken in
// NFKC and case folded, backslashes count as separators, lookalikes of the dot
// count as dots, and trailing dots, spaces and a stream suffix after ':' are
// dropped. The web host has the same function (isCodeFile in pagesBridge.js);
// both run testdata/code-files.json so they cannot drift. The store has the
// same judgement (workspace.IsCodeFile) for the grants of mode data.
func IsCodeFile(rel string) bool { return workspace.IsCodeFile(rel) }
