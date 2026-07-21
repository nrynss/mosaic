package api

import (
	"net/http"
	"strings"

	"mosaic.local/mosaic/internal/democast"
)

// handleDemoInteractions serves the recording-manifest operator steps as
// ready-to-POST payloads for the demo UI. Read-only; no secrets.
func (s *Server) handleDemoInteractions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadDemoInteractions) {
		return
	}
	root := strings.TrimSpace(s.demoAssetRoot)
	if root == "" {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{
			Code:    "demo_interactions_unavailable",
			Message: "demo asset root is not configured",
		})
		return
	}
	doc, err := democast.BuildInteractions(root)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "demo_interactions_load_failed",
			Message: "could not load demo recording interactions",
		})
		return
	}
	// Surface process cassette mode so the UI can label provenance honestly.
	writeJSON(w, http.StatusOK, map[string]any{
		"scenario":              doc.Scenario,
		"expected_cop_revision": doc.ExpectedCOPRevision,
		"supervisor_identity":   doc.SupervisorIdentity,
		"cassette_mode":         s.effectiveCassetteMode(),
		"steps":                 doc.Steps,
	}, nil)
}
