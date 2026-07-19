package handlers

import (
	"net/http"
	"strconv"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/realtime"
	"momopay/internal/session"
	"momopay/internal/txn"
	"momopay/internal/webhook"
)

// registerAdminRoutes exposes the staff back office: login, a live
// dashboard of every transaction, validate/reject actions and the ability
// to create a payment link on behalf of any merchant. Merchant account
// management lives in merchants.go.
func registerAdminRoutes(se *core.ServeEvent, app core.App, hub *realtime.Hub, store *session.Store) {
	g := se.Router.Group("/admin")
	g.BindFunc(store.LoadAuth())

	g.GET("/login", adminLoginPage())
	g.POST("/login", adminLoginSubmit(store))

	protected := g.Group("")
	protected.BindFunc(requireAuthOrRedirect("admins", "/admin/login"))
	protected.BindFunc(csrfMiddleware(store))

	protected.POST("/logout", adminLogout(store))
	protected.GET("", adminDashboard(app, store))
	protected.GET("/stream", adminStream(hub))
	protected.POST("/transactions", adminCreateTransaction(app))
	protected.GET("/transactions/{id}", adminTransactionDetail(app))
	protected.POST("/transactions/{id}/validate", adminResolveTransaction(app, true))
	protected.POST("/transactions/{id}/reject", adminResolveTransaction(app, false))

	registerAdminMerchantRoutes(protected, app)
	registerAdminReconcileRoutes(protected, app, hub)
}

// csrfMiddleware rejects state-changing (non-GET) requests whose
// X-CSRF-Token header (or csrf_token form field) doesn't match the
// double-submit cookie. See session.ValidateCSRF for the full rationale.
func csrfMiddleware(store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Request.Method != http.MethodGet && !store.ValidateCSRF(e.Request) {
			return e.ForbiddenError("Invalid or missing CSRF token.", nil)
		}
		return e.Next()
	}
}

func adminLoginPage() func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth != nil {
			return e.Redirect(http.StatusFound, "/admin")
		}
		return renderLogin(e, "admin_login", "Connexion administrateur", "")
	}
}

func adminLoginSubmit(store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		email := e.Request.FormValue("email")
		password := e.Request.FormValue("password")

		if _, err := store.Login(e.Response, email, password); err != nil {
			return renderLogin(e, "admin_login", "Connexion administrateur", "Email ou mot de passe invalide.")
		}

		return e.Redirect(http.StatusFound, "/admin")
	}
}

func adminLogout(store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		store.Logout(e.Response)
		return e.Redirect(http.StatusFound, "/admin/login")
	}
}

func renderLogin(e *core.RequestEvent, page, title, loginErr string) error {
	data := struct {
		Layout
		Error string
	}{Layout: Layout{Title: title}, Error: loginErr}

	status := http.StatusOK
	if loginErr != "" {
		status = http.StatusUnauthorized
	}
	return renderPageStatus(e, page, status, data)
}

func adminDashboard(app core.App, store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		records, err := app.FindRecordsByFilter("transactions", "", "-created", 200, 0)
		if err != nil {
			return e.InternalServerError("Failed to load transactions.", err)
		}
		rows := make([]TxnRow, len(records))
		for i, r := range records {
			rows[i] = txnRowData(app, r, true)
		}

		merchants, err := app.FindRecordsByFilter("merchants", "status = 'active'", "name", 200, 0)
		if err != nil {
			return e.InternalServerError("Failed to load merchants.", err)
		}
		merchantOpts := make([]MerchantOption, len(merchants))
		for i, m := range merchants {
			merchantOpts[i] = merchantOptionData(m)
		}

		data := struct {
			Layout
			Rows      []TxnRow
			Merchants []MerchantOption
		}{
			Layout:    Layout{Title: "Tableau de bord", CSRFToken: store.CSRFToken(e.Request)},
			Rows:      rows,
			Merchants: merchantOpts,
		}

		return renderPage(e, "admin_dashboard", data)
	}
}

func adminStream(hub *realtime.Hub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return serveSSE(e, hub, "admin")
	}
}

// adminTransactionDetail renders the "Détails" modal content for one
// transaction, opened by clicking a row (see txn_row.html) and swapped into
// the #dialog placeholder declared once in layout.html.
func adminTransactionDetail(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById("transactions", e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Transaction introuvable.", nil)
		}
		return renderDialog(e, txnRowData(app, record, true))
	}
}

// adminCreateTransaction creates a payment link on behalf of a merchant.
// The response body is discarded by the caller (hx-swap="none"): the new
// row reaches every open admin dashboard, including the actor's own,
// through the "txn-new" SSE broadcast — see BindRealtimeHooks.
func adminCreateTransaction(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchantID := e.Request.FormValue("merchant")
		amount, err := strconv.ParseFloat(e.Request.FormValue("amount"), 64)
		if err != nil || amount <= 0 || merchantID == "" {
			return e.BadRequestError("Merci de renseigner un marchand et un montant valide.", nil)
		}

		if _, err := app.FindRecordById("merchants", merchantID); err != nil {
			return e.BadRequestError("Marchand introuvable.", nil)
		}

		_, err = txn.Create(app, txn.CreateParams{
			MerchantID:  merchantID,
			Amount:      amount,
			Description: e.Request.FormValue("description"),
			Mode:        txn.ModeLive,
			CreatedBy:   txn.CreatedByAdmin,
		})
		if err != nil {
			return e.InternalServerError("Failed to create transaction.", err)
		}

		return e.NoContent(http.StatusCreated)
	}
}

// adminResolveTransaction validates or rejects a submitted transaction and
// (best-effort, async) notifies the merchant's webhook. Like create, the
// response is discarded (hx-swap="none") — the row update reaches every
// open dashboard via the "txn-update" SSE broadcast.
func adminResolveTransaction(app core.App, approve bool) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById("transactions", e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Transaction introuvable.", nil)
		}

		adminEmail := e.Auth.GetString("email")
		note := e.Request.FormValue("note")
		if note == "" {
			// populated when the button uses hx-prompt to ask for a reason
			note = e.Request.Header.Get("HX-Prompt")
		}

		if err := txn.Resolve(app, record, approve, adminEmail, note); err != nil {
			return e.BadRequestError("Cette transaction n'est plus en attente de validation.", nil)
		}

		dispatchWebhook(app, record)

		return e.NoContent(http.StatusOK)
	}
}

func dispatchWebhook(app core.App, record *core.Record) {
	merchant, err := app.FindRecordById("merchants", record.GetString("merchant"))
	if err != nil {
		return
	}
	url := merchant.GetString("webhook_url")
	if url == "" {
		return
	}

	payload := webhook.Payload{
		ID:             record.Id,
		Status:         record.GetString("status"),
		Amount:         record.GetFloat("amount"),
		Currency:       record.GetString("currency"),
		Description:    record.GetString("description"),
		Metadata:       txn.Metadata(record),
		ResolutionNote: record.GetString("resolution_note"),
		OccurredAt:     record.GetDateTime("resolved_at").String(),
	}

	go webhook.Dispatch(app, record.Id, url, merchant.GetString("webhook_secret"), payload)
}
