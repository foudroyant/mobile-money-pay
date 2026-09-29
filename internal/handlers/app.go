package handlers

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/payid"
	"momopay/internal/realtime"
	"momopay/internal/session"
	"momopay/internal/stats"
	"momopay/internal/view"
)

// registerAppRoutes exposes the small merchant self-service dashboard
// (/app): merchants can log in with the email+password they were given
// when their account was created and create payment links themselves,
// as an alternative to the /v1 API — the same way Stripe lets you create
// a Payment Link either from the Dashboard or from the API.
func registerAppRoutes(se *core.ServeEvent, app core.App, hub *realtime.Hub, store *session.Store) {
	g := se.Router.Group("/app")
	g.BindFunc(store.LoadAuth())

	g.GET("/login", appLoginPage())
	g.POST("/login", appLoginSubmit(store))

	protected := g.Group("")
	protected.BindFunc(requireAuthOrRedirect("merchants", "/app/login"))
	protected.BindFunc(csrfMiddleware(store))

	protected.POST("/logout", appLogout(store))
	protected.GET("", appDashboard(app, store))
	protected.GET("/stream", appStream(hub))
	protected.GET("/transactions/{id}", appTransactionDetail(app))
	protected.GET("/stats", appStats(app, store))
	protected.POST("/rotate-key", appRotateKey(app))
	protected.POST("/webhook", appUpdateWebhook(app))

	registerAppLinkRoutes(protected, app, hub, store)
}

func appLoginPage() func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth != nil {
			return e.Redirect(http.StatusFound, "/app")
		}
		return renderLogin(e, "app_login", "Connexion marchand", "")
	}
}

func appLoginSubmit(store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		email := e.Request.FormValue("email")
		password := e.Request.FormValue("password")

		if _, err := store.Login(e.Response, email, password); err != nil {
			return renderLogin(e, "app_login", "Connexion marchand", "Email ou mot de passe invalide.")
		}

		return e.Redirect(http.StatusFound, "/app")
	}
}

func appLogout(store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		store.Logout(e.Response)
		return e.Redirect(http.StatusFound, "/app/login")
	}
}

func appDashboard(app core.App, store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		records, err := app.FindRecordsByFilter(
			"transactions", "merchant = {:m}", "-created", 200, 0,
			map[string]any{"m": e.Auth.Id},
		)
		if err != nil {
			return e.InternalServerError("Failed to load transactions.", err)
		}
		rows := make([]TxnRow, len(records))
		for i, r := range records {
			rows[i] = txnRowData(app, r, false)
		}

		data := struct {
			Layout
			Rows          []TxnRow
			KeyPrefix     string
			WebhookURL    string
			WebhookSecret string
		}{
			Layout:        Layout{Title: "Mon espace marchand", CSRFToken: store.CSRFToken(e.Request)},
			Rows:          rows,
			KeyPrefix:     e.Auth.GetString("api_key_prefix"),
			WebhookURL:    e.Auth.GetString("webhook_url"),
			WebhookSecret: e.Auth.GetString("webhook_secret"),
		}

		return renderPage(e, "app_dashboard", data)
	}
}

func appStream(hub *realtime.Hub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return serveSSE(e, hub, "merchant:"+e.Auth.Id)
	}
}

// appStats is the merchant-facing counterpart of adminMerchantStats
// (merchants.go) — same sales summary and 30-day evolution chart, scoped
// to the logged-in merchant's own account.
func appStats(app core.App, store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		summary, err := stats.ForMerchant(app, e.Auth.Id, statsWindowDays)
		if err != nil {
			return e.InternalServerError("Failed to compute stats.", err)
		}

		data := struct {
			Layout
			StatsView
		}{
			Layout:    Layout{Title: "Statistiques", CSRFToken: store.CSRFToken(e.Request)},
			StatsView: statsViewData(e.Auth.GetString("name"), summary),
		}

		return renderPage(e, "app_stats", data)
	}
}

// appTransactionDetail is the merchant-facing counterpart of
// adminTransactionDetail — same #dialog target, but read-only (CanResolve
// false) and scoped to the merchant's own transactions.
func appTransactionDetail(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById("transactions", e.Request.PathValue("id"))
		if err != nil || record.GetString("merchant") != e.Auth.Id {
			return e.NotFoundError("Transaction introuvable.", nil)
		}
		return renderDialog(e, txnRowData(app, record, false))
	}
}

// appUpdateWebhook sets (or clears) the merchant's webhook callback URL.
// A signing secret is generated the first time a URL is set so
// internal/webhook.Dispatch can sign notifications for it.
func appUpdateWebhook(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		url := e.Request.FormValue("webhook_url")

		record := e.Auth
		record.Set("webhook_url", url)
		if url != "" && record.GetString("webhook_secret") == "" {
			secret, err := payid.GenerateWebhookSecret()
			if err != nil {
				return e.InternalServerError("Failed to generate webhook secret.", err)
			}
			record.Set("webhook_secret", secret)
		}
		if url == "" {
			record.Set("webhook_secret", "")
		}

		if err := app.Save(record); err != nil {
			return e.BadRequestError("URL de webhook invalide.", err)
		}

		return e.Redirect(http.StatusFound, "/app")
	}
}

func appRotateKey(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		key, err := payid.GenerateAPIKey(true)
		if err != nil {
			return e.InternalServerError("Failed to generate API key.", err)
		}

		record := e.Auth
		record.Set("api_key_hash", key.Hash)
		record.Set("api_key_prefix", key.Prefix)
		if err := app.Save(record); err != nil {
			return e.InternalServerError("Failed to rotate API key.", err)
		}

		html, err := view.Partial("app_key_reveal", key.Secret)
		if err != nil {
			return e.InternalServerError("Failed to render page.", err)
		}
		return e.HTML(http.StatusOK, html)
	}
}
