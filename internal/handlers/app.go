package handlers

import (
	"net/http"
	"strconv"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/payid"
	"momopay/internal/realtime"
	"momopay/internal/session"
	"momopay/internal/txn"
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
	protected.POST("/transactions", appCreateTransaction(app))
	protected.GET("/transactions/{id}", appTransactionDetail(app))
	protected.POST("/rotate-key", appRotateKey(app))
	protected.POST("/webhook", appUpdateWebhook(app))
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

// appCreateTransaction creates a payment link for the logged-in merchant.
// As with the admin dashboard, the response is discarded (hx-swap="none")
// and the new row reaches the dashboard through the SSE broadcast instead,
// so the actor doesn't see the row inserted twice.
func appCreateTransaction(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		amount, err := strconv.ParseFloat(e.Request.FormValue("amount"), 64)
		if err != nil || amount <= 0 {
			return e.BadRequestError("Merci de renseigner un montant valide.", nil)
		}

		_, err = txn.Create(app, txn.CreateParams{
			MerchantID:  e.Auth.Id,
			Amount:      amount,
			Description: e.Request.FormValue("description"),
			SuccessURL:  e.Request.FormValue("success_url"),
			CancelURL:   e.Request.FormValue("cancel_url"),
			Mode:        txn.ModeLive,
			CreatedBy:   txn.CreatedByMerchant,
		})
		if err != nil {
			return e.InternalServerError("Failed to create transaction.", err)
		}

		return e.NoContent(http.StatusCreated)
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
