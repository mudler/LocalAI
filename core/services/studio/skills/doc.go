// Package skills finds the skills that apply to a chat and renders the index
// that goes into the prompt.
//
// A skill is a folder with a SKILL.md file: a short description in the
// frontmatter and a procedure in the body. Skills live in workspace folders in
// five layers: built-in, add-on, shared, personal and folder. [Resolve] reads them
// through the actor, so a folder that the actor cannot read gives no skill. It
// applies the precedence rule (the nearest layer wins) and marks the skills it
// skipped. [Index] renders the skills that remain as a short list of names,
// descriptions and guest paths. The agent reads the full SKILL.md with its
// normal file tools, and writes its own skills the same way.
//
// The package imports the standard library and workspace only. It does not
// name a product, a sandbox or a harness: the product tells it which folders
// are the roots of the layers and where the guest sees each of them.
package skills
