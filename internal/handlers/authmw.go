package handlers

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

// requireAuthOrRedirect is like apis.RequireAuth(collectionName) but sends
// browsers to loginPath instead of returning a bare JSON 401 — the /admin
// and /app groups are HTML back offices, not API endpoints, so an
// unauthenticated visit should land on the login form.
func requireAuthOrRedirect(collectionName, loginPath string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil || e.Auth.Collection().Name != collectionName {
			return e.Redirect(http.StatusFound, loginPath)
		}
		return e.Next()
	}
}
