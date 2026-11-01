# AGENTS.md

## Checks

Run all of these and make sure they pass before committing or pushing:

```sh
go build ./...
go vet ./...
go test -race ./...
golangci-lint run
```

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues for andrewheberle/geoip-spoa. See `docs/agents/issue-tracker.md`.

### Triage labels

Default label vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
