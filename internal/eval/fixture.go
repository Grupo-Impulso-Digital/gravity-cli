// Package eval scores dry-run recordings of the model quality eval fixture.
package eval

// Endpoint is one operation of the fixture's REST API.
type Endpoint struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Internal bool   `json:"internal,omitempty"`
}

// String renders the endpoint as "METHOD /path".
func (e Endpoint) String() string { return e.Method + " " + e.Path }

// FixtureEndpoints are the 17 operations testdata/eval/make-fixture.sh writes to api/openapi.yaml.
var FixtureEndpoints = []Endpoint{
	{Method: "GET", Path: "/topics"},
	{Method: "POST", Path: "/topics"},
	{Method: "GET", Path: "/topics/{topicId}"},
	{Method: "PATCH", Path: "/topics/{topicId}"},
	{Method: "DELETE", Path: "/topics/{topicId}"},
	{Method: "GET", Path: "/topics/{topicId}/posts"},
	{Method: "POST", Path: "/topics/{topicId}/posts"},
	{Method: "GET", Path: "/posts/{postId}"},
	{Method: "PATCH", Path: "/posts/{postId}"},
	{Method: "DELETE", Path: "/posts/{postId}"},
	{Method: "POST", Path: "/topics/{topicId}/subscriptions"},
	{Method: "DELETE", Path: "/topics/{topicId}/subscriptions/{subscriptionId}"},
	{Method: "GET", Path: "/tags"},
	{Method: "GET", Path: "/search"},
	{Method: "POST", Path: "/topics/{topicId}/legacy-import", Internal: true},
	{Method: "POST", Path: "/internal/reindex", Internal: true},
	{Method: "GET", Path: "/internal/debug/cache", Internal: true},
}

// Public keeps the endpoints that are not internal.
func Public(endpoints []Endpoint) []Endpoint {
	var out []Endpoint
	for _, e := range endpoints {
		if !e.Internal {
			out = append(out, e)
		}
	}
	return out
}

// Internal keeps the internal endpoints.
func Internal(endpoints []Endpoint) []Endpoint {
	var out []Endpoint
	for _, e := range endpoints {
		if e.Internal {
			out = append(out, e)
		}
	}
	return out
}
