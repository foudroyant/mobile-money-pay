// Package handlers wires up every HTTP route exposed by the platform:
// the public merchant REST API (/v1), the hosted payment page (/pay),
// the merchant dashboard (/app) and the admin back office (/admin).
package handlers

import (
	"net/http"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/realtime"
	"momopay/internal/session"
	"momopay/static"
)

// Register attaches every route group to the router inside app.OnServe().
func Register(se *core.ServeEvent, app *pocketbase.PocketBase, hub *realtime.Hub) {
	se.Router.GET("/static/{path...}", apis.Static(static.FS, false))

	se.Router.GET("/", func(e *core.RequestEvent) error {
		return e.Redirect(http.StatusFound, "/admin")
	})

	// Closes the #dialog popup opened by clicking a transaction row (see
	// txn_detail.html) — shared by /admin and /app, so it isn't gated behind
	// either auth group; it never reveals anything, it only clears a target.
	se.Router.GET("/dialog/close", func(e *core.RequestEvent) error {
		return e.HTML(http.StatusOK, "")
	})

	adminStore := &session.Store{App: app, CollectionName: "admins", CookieName: "admin_session", CSRFCookieName: "admin_csrf"}
	merchantStore := &session.Store{App: app, CollectionName: "merchants", CookieName: "merchant_session", CSRFCookieName: "merchant_csrf"}

	registerAPIRoutes(se, app)
	registerPayRoutes(se, app, hub)
	registerAdminRoutes(se, app, hub, adminStore)
	registerAppRoutes(se, app, hub, merchantStore)
}
