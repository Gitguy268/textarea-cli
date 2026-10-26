# Contributing

Thanks for helping with textarea-cli.

By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting a bug or asking for a feature

Open an [issue](https://github.com/Gitguy268/textarea-cli/issues/new/choose) and use the template. For a bug, say what you ran (the menu or a `text` command), which sync target if any, your OS and terminal, what you expected and what happened.

Report security problems privately: see [SECURITY.md](SECURITY.md).

## Making a change

1. Fork the repository and create a branch from `main`.
2. Build and run it. You need Go 1.27 or newer.

   ```sh
   go build -o text ./cmd/text
   TEXT_LIBRARY=/tmp/text-lib XDG_CONFIG_HOME=/tmp/text-cfg ./text
   ```

   `TEXT_LIBRARY` and `XDG_CONFIG_HOME` keep a test run away from your own notes and settings.

3. Make your change. Keep commits small, each with a clear message.
4. Run the checks below and update the README if behaviour changed.
5. Open a pull request and fill in the template.

## Checks

```sh
gofmt -l .          # prints nothing
go vet ./...
go test ./...
```

### The oracle tests

The notes format, share links, exports and the sync plan must match the textarea web app byte for byte. `internal/golden` checks this by running the web app's own JavaScript under Node (`testdata/oracle.mjs`). It needs Node and a textarea tree: by default `../textarea-master-claude`, or set `TEXTAREA_DIR`.

Without them, the tests replay the answers recorded in `testdata/oracle/`, and the two that can't be replayed (encrypted links, the web-app interop run) skip. When the web app changes on purpose, refresh the recordings:

```sh
ORACLE_RECORD=1 go test ./internal/golden
```

A live run fails when a recording is stale, so a change on either side shows up.

## Layout

| Package | What it does |
|---|---|
| `cmd/text` | the `text` command: the menu, quick capture and the scripting subcommands |
| `internal/note` | the note file: front matter, titles, file names, word counts |
| `internal/share` | share links, plain and password-protected |
| `internal/store` | the library folder, plus pin, archive and trash state in `.textarea/` |
| `internal/syncplan` | the sync decision table, shared by every target |
| `internal/target` | folder, Obsidian and Apple Notes targets |
| `internal/export` | the nine export formats |
| `internal/filter`, `internal/markdown` | search grammar, tags, tasks, wikilinks |
| `internal/ai` | optional whisper.cpp, llama.cpp and Ollama adapters |
| `internal/ui` | the Bubble Tea screens |
| `internal/oracle`, `internal/golden` | the cross-language tests |
