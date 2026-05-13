---
name: mom-project
description: Bind the current directory to a MOM project so captured memories are scoped to it. Use when the user wants to set up project scoping, bind a repo, declare a project id, rebind a directory, or asks about .mom-project.yaml.
user-invocable: true
allowed-tools: Bash(mom project*), Bash(cat .mom-project.yaml*), Bash(test -f .mom-project.yaml*)
---

Run only after explicit user request (or when the user accepts a nudge from `mom status` about an unbound directory).

Per ADR 0016 the file `.mom-project.yaml` at a project root declares its identity. Memories captured from a bound directory carry the declared id; recall scopes to that id by default.

## Flow

1. Check whether the current directory is already bound:

```bash
test -f .mom-project.yaml && cat .mom-project.yaml
```

If the file exists, show the current id and ask the user one of:
- keep the binding (do nothing)
- rebind to a different id (note that this does NOT merge old memories — changing the id starts a fresh project from MOM's perspective)

If the file does not exist, continue.

2. Propose an `id`. Default = the directory's basename, normalised (lowercase, trim, replace spaces with dashes). Show the proposal and ask the user to confirm or override.

The id is an opaque string MOM stores verbatim. Help the user pick one that is specific enough for their sharing context — git remotes are not consulted, so two unrelated projects on different machines can pick the same id and collide if they ever sync. Suggest something like `vendor/project` or `team-service` if that matters for them.

3. Write the binding:

```bash
mom project bind --id <chosen-id>
```

If the directory already declares a different id and the user agreed to rebind, append `--force`.

4. Suggest committing the file:

```
Check `.mom-project.yaml` into version control so the binding travels with the repo.
```

5. Do not silently rebind. The id is a shared artifact; the user should pick it deliberately.

## Behavior rules

- Never invoke `mom project bind` without confirming the id with the user.
- Never run `mom init` from this skill — `mom init` is the global install flow and does not write project bindings (ADR 0016).
- If `mom project bind` returns an error, surface the error to the user and stop.
- After a successful bind, capture a one-line confirmation that the directory is now bound to `<id>`.
