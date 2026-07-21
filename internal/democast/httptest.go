package democast

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// HandlerClient adapts an http.Handler to *http.Client via httptest, so in-process
// mosaicdemo handlers can be driven with the same Driver as a subprocess URL.
func HandlerClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			// httptest needs a non-nil URL path; preserve method/body/headers.
			w := httptest.NewRecorder()
			// Clone request so ServeHTTP can safely consume the body.
			r2 := req.Clone(req.Context())
			if r2.URL != nil && r2.URL.Scheme == "" {
				// Ensure RequestURI/path is usable by the mux.
				if r2.URL.Path == "" && strings.HasPrefix(req.URL.String(), "http") {
					// already absolute
				}
			}
			handler.ServeHTTP(w, r2)
			resp := w.Result()
			// Result() body is already open; Client.Do callers will close it.
			return resp, nil
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
