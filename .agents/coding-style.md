# Coding Style

The project has the following .editorconfig:

```
root = true

[*]
indent_style = space
indent_size = 2
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true

[*.go]
indent_style = tab

[Makefile]
indent_style = tab

[*.proto]
indent_size = 2

[*.py]
indent_size = 4

[*.js]
indent_size = 2

[*.yaml]
indent_size = 2

[*.md]
trim_trailing_whitespace = false
```

- Use comments sparingly to explain why code does something, not what it does. Comments are there to add context that would be difficult to deduce from reading the code.
- Prefer modern Go e.g. use `any` not `interface{}`

## Logging

Use `github.com/mudler/xlog` for logging which has the same API as slog.

## Documentation

The project documentation is located in `docs/content`. When adding new features or changing existing functionality, it is crucial to update the documentation to reflect these changes. This helps users understand how to use the new capabilities and ensures the documentation stays relevant.

- **Feature Documentation**: If you add a new feature (like a new backend or API endpoint), create a new markdown file in `docs/content/features/` explaining what it is, how to configure it, and how to use it.
- **Configuration**: If you modify configuration options, update the relevant sections in `docs/content/`.
- **Examples**: providing concrete examples (like YAML configuration blocks) is highly encouraged to help users get started quickly.
