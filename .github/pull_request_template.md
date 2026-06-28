<!-- Thanks for contributing to zipfast! Keep PRs focused and small where possible. -->

## What & why

<!-- A short description of the change and the motivation. Link any issue: Closes #123 -->

## Type of change

- [ ] Bug fix
- [ ] New feature / parity with upstream Zipline
- [ ] Refactor / tech debt
- [ ] CI / build / docs

## Zipline parity

<!-- If this ports an upstream change, link it. Bump ZIPLINE_PARITY in a separate
     commit only when full parity with a new Zipline release is reached. -->

- Upstream reference:
- Updates `ZIPLINE_PARITY`? **no** / yes → `x.y.z`

## Checklist

- [ ] `make check` passes locally (gofmt, `go vet`, `go test`)
- [ ] Web SPA still builds (`make web`) if the client was touched
- [ ] No secrets, tokens, or private data added to code or logs
- [ ] Updated docs / README where relevant
