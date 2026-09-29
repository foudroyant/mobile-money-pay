package handlers

import (
	"errors"
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/paylink"
	"momopay/internal/realtime"
	"momopay/internal/txn"
	"momopay/internal/view"
)

var errTxnNotForLink = errors.New("transaction does not belong to this payment link")

// registerPayRoutes exposes the public, unauthenticated hosted payment
// page. Knowledge of the unguessable {token} is the only access control
// here, the same trust model as e.g. Stripe Checkout links. A link's token
// is shared by every payer who uses it (see internal/paylink) — each
// submission creates its own transaction, scoped by its own id for the
// live status view and SSE stream below.
func registerPayRoutes(se *core.ServeEvent, app core.App, hub *realtime.Hub) {
	se.Router.GET("/pay/{token}", payShow(app))
	se.Router.GET("/pay/{token}/image", payImage(app))
	se.Router.POST("/pay/{token}/submit", paySubmit(app))
	se.Router.POST("/pay/{token}/tx/{id}/resubmit", payResubmit(app))
	se.Router.GET("/pay/{token}/tx/{id}/stream", payStream(app, hub))
}

func findLinkByToken(app core.App, token string) (*core.Record, error) {
	return app.FindFirstRecordByFilter("payment_links", "token = {:t}", dbx.Params{"t": token})
}

// findTxnForToken loads a transaction and checks it was actually made
// against the link identified by token, so one payer can't poke at another
// payer's transaction id even though ids aren't otherwise guessable.
func findTxnForToken(app core.App, token, id string) (*core.Record, error) {
	link, err := findLinkByToken(app, token)
	if err != nil {
		return nil, err
	}
	record, err := app.FindRecordById("transactions", id)
	if err != nil {
		return nil, err
	}
	if record.GetString("payment_link") != link.Id {
		return nil, errTxnNotForLink
	}
	return record, nil
}

func payShow(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		link, err := findLinkByToken(app, e.Request.PathValue("token"))
		if err != nil || !paylink.IsUsable(link) {
			// Same "unavailable" response whether the token never existed
			// or the link is unpublished/revoked, so a bad guess can't be
			// used to probe for valid-but-hidden tokens.
			return renderPageStatus(e, "pay_unavailable", http.StatusNotFound, Layout{Title: "Lien indisponible"})
		}

		data := struct {
			Layout
			PayLinkView
		}{
			Layout:      Layout{Title: "Payer avec Airtel Money"},
			PayLinkView: payLinkViewData(app, link),
		}

		return renderPage(e, "pay", data)
	}
}

// payImage streams a link's uploaded image. Served regardless of the
// link's status (unpublished/revoked links still need their thumbnail to
// render correctly in the admin/merchant dashboards) — the unguessable
// token is the only gate, same as everywhere else on this public surface.
func payImage(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		link, err := findLinkByToken(app, e.Request.PathValue("token"))
		filename := ""
		if err == nil {
			filename = link.GetString("image")
		}
		if filename == "" {
			return e.NotFoundError("Image introuvable.", nil)
		}

		fsys, err := app.NewFilesystem()
		if err != nil {
			return e.InternalServerError("Filesystem initialization failure.", err)
		}
		defer fsys.Close()

		if err := fsys.Serve(e.Response, e.Request, link.BaseFilesPath()+"/"+filename, filename); err != nil {
			return e.NotFoundError("Image introuvable.", err)
		}
		return nil
	}
}

func paySubmit(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		link, err := findLinkByToken(app, e.Request.PathValue("token"))
		if err != nil || !paylink.IsUsable(link) {
			return e.NotFoundError("Lien de paiement introuvable ou indisponible.", nil)
		}

		msisdn := e.Request.FormValue("phone")
		ref := e.Request.FormValue("operator_ref")
		if msisdn == "" || ref == "" {
			return e.BadRequestError("Merci de renseigner le numéro de téléphone et l'ID de transaction.", nil)
		}

		record, err := txn.CreateFromLink(app, link)
		if err != nil {
			return e.InternalServerError("Failed to create transaction.", err)
		}
		if err := txn.Submit(app, record, msisdn, ref); err != nil {
			return e.InternalServerError("Failed to submit transaction.", err)
		}

		return renderPayStatus(app, e, record)
	}
}

// payResubmit lets a payer correct and resend a rejected submission,
// without creating a second transaction row for the same attempt.
func payResubmit(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := findTxnForToken(app, e.Request.PathValue("token"), e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Transaction introuvable.", nil)
		}

		msisdn := e.Request.FormValue("phone")
		ref := e.Request.FormValue("operator_ref")
		if msisdn == "" || ref == "" {
			return e.BadRequestError("Merci de renseigner le numéro de téléphone et l'ID de transaction.", nil)
		}

		if err := txn.Submit(app, record, msisdn, ref); err != nil {
			return e.BadRequestError("Cette transaction n'accepte plus de nouvelle soumission.", nil)
		}

		return renderPayStatus(app, e, record)
	}
}

// renderPayStatus renders the "pay_status" wrapper (the SSE-connected
// element scoped to this one transaction) as the htmx response replacing
// #pay-status — see templates/pages/pay.html and partials/pay_status.html.
func renderPayStatus(app core.App, e *core.RequestEvent, record *core.Record) error {
	html, err := view.Partial("pay_status", payStatusData(app, record))
	if err != nil {
		return e.InternalServerError("Failed to render page.", err)
	}
	return e.HTML(http.StatusOK, html)
}

func payStream(app core.App, hub *realtime.Hub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := findTxnForToken(app, e.Request.PathValue("token"), e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Transaction introuvable.", nil)
		}
		return serveSSE(e, hub, "txn:"+record.Id)
	}
}
