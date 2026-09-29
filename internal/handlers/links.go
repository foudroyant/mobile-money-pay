package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/router"

	"momopay/internal/paylink"
	"momopay/internal/realtime"
	"momopay/internal/session"
)

// registerAdminLinkRoutes adds payment link ("product") management under
// the already-authenticated /admin group: an admin can create a link on
// behalf of any merchant and change any link's status. Merchants manage
// their own links from the /app counterpart below — both share the same
// paylink.Create/SetStatus logic and row templates (see link_row.html).
func registerAdminLinkRoutes(g *router.RouterGroup[*core.RequestEvent], app core.App, hub *realtime.Hub, store *session.Store) {
	g.GET("/links", adminLinksPage(app, store))
	g.GET("/links/stream", adminLinksStream(hub))
	g.POST("/links", adminCreateLink(app))
	g.POST("/links/{id}/publish", adminSetLinkStatus(app, paylink.StatusPublished))
	g.POST("/links/{id}/unpublish", adminSetLinkStatus(app, paylink.StatusUnpublished))
	g.POST("/links/{id}/revoke", adminSetLinkStatus(app, paylink.StatusRevoked))
}

// registerAppLinkRoutes is the merchant-scoped counterpart: every action is
// restricted to links owned by the logged-in merchant.
func registerAppLinkRoutes(g *router.RouterGroup[*core.RequestEvent], app core.App, hub *realtime.Hub, store *session.Store) {
	g.GET("/links", appLinksPage(app, store))
	g.GET("/links/stream", appLinksStream(hub))
	g.POST("/links", appCreateLink(app))
	g.POST("/links/{id}/publish", appSetLinkStatus(app, paylink.StatusPublished))
	g.POST("/links/{id}/unpublish", appSetLinkStatus(app, paylink.StatusUnpublished))
	g.POST("/links/{id}/revoke", appSetLinkStatus(app, paylink.StatusRevoked))
}

func adminLinksPage(app core.App, store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		records, err := app.FindRecordsByFilter("payment_links", "", "-created", 200, 0)
		if err != nil {
			return e.InternalServerError("Failed to load payment links.", err)
		}
		rows := make([]LinkRow, len(records))
		for i, r := range records {
			rows[i] = linkRowData(app, r, true)
		}

		merchants, err := loadMerchantOptions(app)
		if err != nil {
			return e.InternalServerError("Failed to load merchants.", err)
		}

		data := struct {
			Layout
			Rows      []LinkRow
			Merchants []MerchantOption
		}{
			Layout:    Layout{Title: "Liens de paiement", CSRFToken: store.CSRFToken(e.Request)},
			Rows:      rows,
			Merchants: merchants,
		}

		return renderPage(e, "admin_links", data)
	}
}

func appLinksPage(app core.App, store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		records, err := app.FindRecordsByFilter(
			"payment_links", "merchant = {:m}", "-created", 200, 0,
			map[string]any{"m": e.Auth.Id},
		)
		if err != nil {
			return e.InternalServerError("Failed to load payment links.", err)
		}
		rows := make([]LinkRow, len(records))
		for i, r := range records {
			rows[i] = linkRowData(app, r, false)
		}

		data := struct {
			Layout
			Rows []LinkRow
		}{
			Layout: Layout{Title: "Mes liens de paiement", CSRFToken: store.CSRFToken(e.Request)},
			Rows:   rows,
		}

		return renderPage(e, "app_links", data)
	}
}

func adminLinksStream(hub *realtime.Hub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return serveSSE(e, hub, "admin-links")
	}
}

func appLinksStream(hub *realtime.Hub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return serveSSE(e, hub, "merchant-links:"+e.Auth.Id)
	}
}

// extractLinkImage reads the optional "image" multipart field. A missing
// field is not an error — the image is optional — but a malformed upload
// (wrong mime type, too large) is surfaced to the caller.
func extractLinkImage(e *core.RequestEvent) (*filesystem.File, error) {
	files, err := e.FindUploadedFiles("image")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, nil
		}
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	return files[0], nil
}

// adminCreateLink creates a payment link on behalf of a merchant. Like the
// rest of the admin dashboard, the response is discarded (hx-swap="none")
// and the new row reaches every open dashboard through the "link-new" SSE
// broadcast instead — see BindLinkRealtimeHooks.
func adminCreateLink(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchantID := e.Request.FormValue("merchant")
		title := e.Request.FormValue("title")
		amount, err := strconv.ParseFloat(e.Request.FormValue("amount"), 64)
		if err != nil || amount <= 0 || title == "" || merchantID == "" {
			return e.BadRequestError("Merci de renseigner un marchand, un titre et un montant valide.", nil)
		}
		if _, err := app.FindRecordById("merchants", merchantID); err != nil {
			return e.BadRequestError("Marchand introuvable.", nil)
		}

		image, err := extractLinkImage(e)
		if err != nil {
			return e.BadRequestError("Image invalide (jpeg/png/webp, 5 Mo max).", err)
		}

		_, err = paylink.Create(app, paylink.CreateParams{
			MerchantID:  merchantID,
			Title:       title,
			Description: e.Request.FormValue("description"),
			Amount:      amount,
			SuccessURL:  e.Request.FormValue("success_url"),
			CancelURL:   e.Request.FormValue("cancel_url"),
			Mode:        paylink.ModeLive,
			CreatedBy:   paylink.CreatedByAdmin,
			Image:       image,
		})
		if err != nil {
			return e.InternalServerError("Failed to create payment link.", err)
		}

		return e.NoContent(http.StatusCreated)
	}
}

// appCreateLink is the merchant self-service counterpart of adminCreateLink.
func appCreateLink(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		title := e.Request.FormValue("title")
		amount, err := strconv.ParseFloat(e.Request.FormValue("amount"), 64)
		if err != nil || amount <= 0 || title == "" {
			return e.BadRequestError("Merci de renseigner un titre et un montant valide.", nil)
		}

		image, err := extractLinkImage(e)
		if err != nil {
			return e.BadRequestError("Image invalide (jpeg/png/webp, 5 Mo max).", err)
		}

		_, err = paylink.Create(app, paylink.CreateParams{
			MerchantID:  e.Auth.Id,
			Title:       title,
			Description: e.Request.FormValue("description"),
			Amount:      amount,
			SuccessURL:  e.Request.FormValue("success_url"),
			CancelURL:   e.Request.FormValue("cancel_url"),
			Mode:        paylink.ModeLive,
			CreatedBy:   paylink.CreatedByMerchant,
			Image:       image,
		})
		if err != nil {
			return e.InternalServerError("Failed to create payment link.", err)
		}

		return e.NoContent(http.StatusCreated)
	}
}

// adminSetLinkStatus lets an admin publish/unpublish/revoke any link.
func adminSetLinkStatus(app core.App, status string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById("payment_links", e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Lien introuvable.", nil)
		}
		if err := paylink.SetStatus(app, record, status); err != nil {
			return e.BadRequestError("Ce lien a été révoqué et ne peut plus changer de statut.", nil)
		}
		return e.NoContent(http.StatusOK)
	}
}

// appSetLinkStatus is the merchant-scoped counterpart of
// adminSetLinkStatus — restricted to the caller's own links.
func appSetLinkStatus(app core.App, status string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById("payment_links", e.Request.PathValue("id"))
		if err != nil || record.GetString("merchant") != e.Auth.Id {
			return e.NotFoundError("Lien introuvable.", nil)
		}
		if err := paylink.SetStatus(app, record, status); err != nil {
			return e.BadRequestError("Ce lien a été révoqué et ne peut plus changer de statut.", nil)
		}
		return e.NoContent(http.StatusOK)
	}
}
