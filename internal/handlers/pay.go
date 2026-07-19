package handlers

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/realtime"
	"momopay/internal/txn"
	"momopay/internal/view"
)

// registerPayRoutes exposes the public, unauthenticated hosted payment page.
// Knowledge of the unguessable {token} is the only access control here,
// the same trust model as e.g. Stripe Checkout links.
func registerPayRoutes(se *core.ServeEvent, app core.App, hub *realtime.Hub) {
	se.Router.GET("/pay/{token}", payShow(app))
	se.Router.POST("/pay/{token}/submit", paySubmit(app))
	se.Router.GET("/pay/{token}/stream", payStream(app, hub))
}

func findByToken(app core.App, token string) (*core.Record, error) {
	return app.FindFirstRecordByFilter("transactions", "token = {:t}", dbx.Params{"t": token})
}

func payShow(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := findByToken(app, e.Request.PathValue("token"))
		if err != nil {
			return e.NotFoundError("Lien de paiement introuvable ou expiré.", nil)
		}

		data := struct {
			Layout
			PayStatus
		}{
			Layout:    Layout{Title: "Payer avec Airtel Money"},
			PayStatus: payStatusData(app, record),
		}

		return renderPage(e, "pay", data)
	}
}

func paySubmit(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := findByToken(app, e.Request.PathValue("token"))
		if err != nil {
			return e.NotFoundError("Lien de paiement introuvable ou expiré.", nil)
		}

		msisdn := e.Request.FormValue("phone")
		ref := e.Request.FormValue("operator_ref")
		if msisdn == "" || ref == "" {
			return e.BadRequestError("Merci de renseigner le numéro de téléphone et l'ID de transaction.", nil)
		}

		if err := txn.Submit(app, record, msisdn, ref); err != nil {
			return e.BadRequestError("Cette transaction n'accepte plus de nouvelle soumission.", nil)
		}

		html, err := view.Partial("pay_status", payStatusData(app, record))
		if err != nil {
			return e.InternalServerError("Failed to render page.", err)
		}
		return e.HTML(http.StatusOK, html)
	}
}

func payStream(app core.App, hub *realtime.Hub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := findByToken(app, e.Request.PathValue("token"))
		if err != nil {
			return e.NotFoundError("Lien de paiement introuvable ou expiré.", nil)
		}
		return serveSSE(e, hub, "txn:"+record.GetString("token"))
	}
}
