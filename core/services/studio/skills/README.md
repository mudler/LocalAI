# skills

`skills` finds the skills that apply to a chat and renders the index that goes
into the prompt. This package holds the mechanism. The chats service holds the data: it
decides which workspace folders are the roots of the layers, which skills a
chat switched off, and how the user sees them.

A skill is a folder with a `SKILL.md` file. The frontmatter has a `name` and a
`description`. The body is a procedure that the agent reads when a task
matches. Other files in the folder (references, scripts) are read by the agent
with its normal file tools. The package never reads them.

```go
roots := []skills.Root{
	{Layer: skills.LayerBuiltin, Path: "/builtin-skills", Guest: "/workspace/shared-builtin-skills"},
	{Layer: skills.LayerShared, Path: "/skills", Guest: "/workspace/shared-skills"},
	{Layer: skills.LayerPersonal, Path: "/users/7/.skills", Guest: "/workspace/home/.skills"},
	{Layer: skills.LayerFolder, Path: "/users/7", Guest: "/workspace/home"},
}
list, notes, err := skills.Resolve(ctx, store, actor, roots, skills.Options{Off: chatSwitches})

// In the ChatTools hook of the session:
return session.TurnTools{
	Prompt:     skills.Index(list),
	Note:       strings.Join(notes, "; "),
	RefreshKey: hashOf(list), // changes when a SKILL.md is new or changed
}, nil
```

The sandbox of a chat sees only the files that existed when it was made.
`RefreshKey` tells `session` when to refresh it: a key that differs from the
key of the turn before refreshes the sandbox (its processes end). A switch
that only removes lines from the index needs no refresh, so it must not
change the key.

`session` does not import `skills`. The product joins them.

## Layers

| Layer | Root `Path` | Meaning |
|---|---|---|
| `LayerBuiltin` | the layer folder | skills that ship with the product |
| `LayerShared` | the layer folder | skills an administrator gives to everyone |
| `LayerPersonal` | the layer folder | the skills of one user |
| `LayerFolder` | a folder to walk | every `.skills` folder below it |

Each layer folder holds one subfolder per skill. For `LayerFolder`, `Resolve`
walks the folder breadth first, at most 6 levels deep and at most 2,000
folders per call, and uses each `.skills` folder it finds. It does not enter a
`.skills` folder, a layer folder that has its own root, or a path in
`Root.Skip` (a product skips a folder that holds many chat uploads, so that it
does not use the budget; a `.skills` folder inside it is not found).
`Options.Logger` gets the number of folders listed, at debug level. A walk that a limit
cuts adds the note `some folders were not searched for skills`.

`Resolve` reads through the actor. A folder that the actor cannot read gives
no skill and no error. It returns an error only when the store fails.

## Precedence

The nearest layer wins: folder, personal, shared, built-in. Between two folder
skills the deeper folder wins; at equal depth the path that sorts first wins.
A skill that loses to one with the same name has `Shadowed` set to the layer
of the winner and is not in the index. `Options.Off` runs before this rule, so
if it turns off only a nearer skill, the one below it is uncovered. The chats
turn off the winner for a plain name and one layer for `layer:name`
(skillsvc.go). A skill that was
switched off has `Off` set.

## Limits

| Item | Limit |
|---|---|
| `SKILL.md` size | 64 KiB (`MaxFileSize`) |
| `name` | `^[a-z0-9][a-z0-9-]{0,63}$` |
| `description` | not empty, at most 1,024 bytes after `Clean` (`MaxDescription`) |
| files read per `Resolve` | 200, nearest layers first; the note is `some skills were not read` |
| index | 100 skills and 16 KiB; a last line says `N more skills not listed` |

`Clean` removes control characters and invisible format characters (Unicode
category Cf, bidi overrides included) and folds runs of white space to one
space. A skill that breaks a limit is returned with `Invalid` set to the
reason. It is not in the index.

## Frontmatter

`Parse` reads a small subset of YAML: `key: value` lines, single or double
quoted values, and block scalars (`|`, `>`) for any key. Unknown keys, such as
`allowed-tools` or `license`, are ignored.

## Safety

A skill is text that the model follows. A document that the agent reads can try
to make it write a harmful skill. The package gives the product what it needs
to show the risk (`Hash`, `Layer`, `Path`); marking new and changed skills,
and undo of the turn that wrote one, are the product's job.
