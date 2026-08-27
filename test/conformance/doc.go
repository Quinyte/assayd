// Package conformance pins the dependency contract this platform's design
// asserts, against the artifact a cluster actually installs.
//
// It exists because of a specific failure. Design 03 was written against
// "agentgateway 2.2" — a release that has never existed — and four rounds of
// internal review could not see it, because the stale documentation path still
// returns HTTP 200 and every citation resolved. What caught it was reading the
// published artifact. These tests make that reading repeatable: if upstream
// changes a field, a bound or a doc comment the design leans on, a test fails
// here and names the design sentence that has gone stale.
//
// The chart tarball in testdata is the pinned artifact, vendored so the suite is
// hermetic and runs in `make test` with no network and no cluster. Behaviour
// that needs a running gateway lives in the cluster-tagged tests instead, and is
// recorded in docs/research/agentgateway-v1.4.1-spike.md.
package conformance
