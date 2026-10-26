# Security policy

## Reporting a vulnerability

Please report security problems privately, not in a public issue.

Use GitHub's private reporting form: <https://github.com/Gitguy268/textarea-cli/security/advisories/new>

Include what you found, how to reproduce it, your OS and terminal, and `text version`. You should get a reply within a week. Please give the fix time to ship before you publish details.

## Scope

The most useful reports are about:

- share links: a crafted `#…` link that crashes `text open-link`, makes it inflate without limit, or decrypts with the wrong password;
- sync: a file in a synced folder, Obsidian vault or Apple Notes note that makes `text sync` write or delete outside the library or the target, or delete a note it did not create;
- the Apple Notes script: input that changes what the JavaScript for Automation script does;
- escape sequences in note text that reach your terminal unfiltered;
- the GitHub Actions workflows in this repository.

text runs whisper.cpp, llama.cpp, Ollama, ffmpeg and sox only when you use speech or writing tools. A bug in those programs belongs with their projects.

## Supported versions

Only the latest version on the `main` branch is supported.
