// Package democast drives the scripted domestic-disturbance demo operator
// timeline from a committed request manifest.
//
// The same manifest powers:
//   - offline stub record→replay identity proofs (cmd/mosaicdemo tests)
//   - gated live re-recording (MOSAIC_RECORD_LIVE=1 e2e)
//   - no-live CI replay against testdata/demo/cassettes
//
// Cassette keys are computed from the request, not the model response. Offline
// stubs prove fingerprint stability; a single live pass only fills response
// content behind the same keys.
package democast
