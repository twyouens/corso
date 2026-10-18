# The k3ep library API: moved

The guide for a consumer writing against `pkg/engine` now lives **inside the Go
module**, next to the package it documents:

- [`src/pkg/engine/README.md`](../src/pkg/engine/README.md)

It was moved there (chunk 0.8, review round 1) so that
`src/pkg/engine/docs_test.go` can `//go:embed` it. That test checks the guide
against the package's exported surface in both directions, and while the guide
sat here - outside the module - a change to it did not invalidate Go's test
cache: locally, and in CI (whose `GOCACHE` is keyed only on `src/go.sum`), a
pull request editing only the guide was handed a cached pass. Embedding the
markdown makes it part of the test binary, so the check cannot be cached past.

This pointer stays because `docs/` is this repository's index of fork
documentation.
