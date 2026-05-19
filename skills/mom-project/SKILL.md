---
name: mom-project
description: Bind the current directory to a MOM project so captured memories are scoped to it. Use when the user wants to set up project scoping, bind a repo, declare a project id, rebind a directory, or asks about .mom-project.yaml.
user-invocable: true
allowed-tools: Bash(mom project*), Bash(cat .mom-project.yaml*), Bash(test -f .mom-project.yaml*), Bash(command -v mom*), Bash(brew install momhq/tap/mom*)
---

Invoking this skill **is** the user's request to set up or change project scoping. Proceed with the flow below immediately — do not ask the user to confirm they want to bind. (The skill still asks for confirmation of the `id` itself in step 2.)

A bound directory has a `.mom-project.yaml` file at its root declaring a project `id`. Memories captured while working in that directory are tagged with that id, and recall scopes to it by default.

## Preflight

Check that `mom` is on PATH:

```bash
command -v mom
```

If it is missing, tell the user MOM is not installed and ask permission to install it:

```text
MOM is not installed. Install it now with Homebrew?
  brew install momhq/tap/mom
Source: https://github.com/momhq/mom
```

If the user agrees, run that command. If the user declines, stop. Do not install MOM without explicit permission.

## Flow

1. Check whether the current directory is already bound:

   ```bash
   test -f .mom-project.yaml && cat .mom-project.yaml
   ```

   If the file exists, show the current `id` and ask the user to either keep it or rebind to a different one. Warn that rebinding does not merge memories from the old id — recall will only see memories tagged with the new id.

   If the file does not exist, continue.

2. Propose an `id`. Default to the current directory's basename, lowercased, trimmed, with spaces replaced by dashes. Show the proposal and ask the user to confirm or pick a different one.

   The id is an opaque label. Suggest something specific (for example `vendor/project` or `team-service`) if the user might sync memories with other machines or teammates — unrelated projects sharing the same id will collide.

3. Write the binding:

   ```bash
   mom project bind --id <chosen-id>
   ```

   If the directory already declares a different id and the user agreed to rebind, append `--force`.

4. Tell the user to commit `.mom-project.yaml` to version control so the binding travels with the repo.

## Rules

- Never run `mom project bind` without first confirming the id with the user.
- Never run `mom init` from this skill.
- If `mom project bind` returns an error, surface the error and stop.
- After a successful bind, confirm in one line that the directory is now bound to `<id>`.

## Postflight (version hint)

Any `mom ...` command may print a banner to stderr like:

```
MOM 0.40.1 available. Run `brew upgrade mom` or `mom self-update`
```

If you see that line, finish the task first, then add one short line at the end of your reply suggesting the upgrade. Do not run the upgrade yourself.
