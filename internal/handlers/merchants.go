package handlers

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"momopay/internal/payid"
	"momopay/internal/view"
)

// MerchantsPanel is the view model for the merchant list + "new API key"
// reveal box. It is rendered both as part of the full /admin/merchants
// page and, on its own, as the htmx response after create/rotate actions.
type MerchantsPanel struct {
	Merchants []MerchantOption
	NewKey    string
	NewName   string
}

// registerAdminMerchantRoutes adds merchant account management under the
// already-authenticated /admin group: list, create (API key shown once),
// rotate key, disable/enable.
func registerAdminMerchantRoutes(g *router.RouterGroup[*core.RequestEvent], app core.App) {
	g.GET("/merchants", adminMerchantsPage(app))
	g.POST("/merchants", adminCreateMerchant(app))
	g.POST("/merchants/{id}/rotate-key", adminRotateMerchantKey(app))
	g.POST("/merchants/{id}/toggle-status", adminToggleMerchantStatus(app))
}

func loadMerchantOptions(app core.App) ([]MerchantOption, error) {
	records, err := app.FindRecordsByFilter("merchants", "", "-created", 200, 0)
	if err != nil {
		return nil, err
	}
	opts := make([]MerchantOption, len(records))
	for i, r := range records {
		opts[i] = merchantOptionData(r)
	}
	return opts, nil
}

func adminMerchantsPage(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		opts, err := loadMerchantOptions(app)
		if err != nil {
			return e.InternalServerError("Failed to load merchants.", err)
		}

		data := struct {
			Layout
			MerchantsPanel
		}{
			Layout:         Layout{Title: "Marchands"},
			MerchantsPanel: MerchantsPanel{Merchants: opts},
		}

		return renderPage(e, "admin_merchants", data)
	}
}

func adminCreateMerchant(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		name := e.Request.FormValue("name")
		email := e.Request.FormValue("email")
		password := e.Request.FormValue("password")
		if name == "" || email == "" || len(password) < 8 {
			return e.BadRequestError("Nom, email et mot de passe (8 caractères min.) sont requis.", nil)
		}

		key, err := payid.GenerateAPIKey(true)
		if err != nil {
			return e.InternalServerError("Failed to generate API key.", err)
		}

		collection, err := app.FindCollectionByNameOrId("merchants")
		if err != nil {
			return e.InternalServerError("Failed to load merchants collection.", err)
		}

		record := core.NewRecord(collection)
		record.Set("name", name)
		record.Set("email", email)
		record.SetPassword(password)
		record.Set("verified", true)
		record.Set("api_key_hash", key.Hash)
		record.Set("api_key_prefix", key.Prefix)
		record.Set("status", "active")

		if err := app.Save(record); err != nil {
			return e.BadRequestError("Impossible de créer ce marchand (email déjà utilisé ?).", err)
		}

		opts, err := loadMerchantOptions(app)
		if err != nil {
			return e.InternalServerError("Failed to load merchants.", err)
		}

		html, err := view.Partial("merchants_panel", MerchantsPanel{Merchants: opts, NewKey: key.Secret, NewName: name})
		if err != nil {
			return e.InternalServerError("Failed to render page.", err)
		}
		return e.HTML(http.StatusOK, html)
	}
}

func adminRotateMerchantKey(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById("merchants", e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Marchand introuvable.", nil)
		}

		key, err := payid.GenerateAPIKey(true)
		if err != nil {
			return e.InternalServerError("Failed to generate API key.", err)
		}
		record.Set("api_key_hash", key.Hash)
		record.Set("api_key_prefix", key.Prefix)
		if err := app.Save(record); err != nil {
			return e.InternalServerError("Failed to rotate API key.", err)
		}

		opts, err := loadMerchantOptions(app)
		if err != nil {
			return e.InternalServerError("Failed to load merchants.", err)
		}

		html, err := view.Partial("merchants_panel", MerchantsPanel{
			Merchants: opts,
			NewKey:    key.Secret,
			NewName:   record.GetString("name"),
		})
		if err != nil {
			return e.InternalServerError("Failed to render page.", err)
		}
		return e.HTML(http.StatusOK, html)
	}
}

func adminToggleMerchantStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById("merchants", e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Marchand introuvable.", nil)
		}

		if record.GetString("status") == "active" {
			record.Set("status", "disabled")
		} else {
			record.Set("status", "active")
		}
		if err := app.Save(record); err != nil {
			return e.InternalServerError("Failed to update merchant.", err)
		}

		opts, err := loadMerchantOptions(app)
		if err != nil {
			return e.InternalServerError("Failed to load merchants.", err)
		}

		html, err := view.Partial("merchants_panel", MerchantsPanel{Merchants: opts})
		if err != nil {
			return e.InternalServerError("Failed to render page.", err)
		}
		return e.HTML(http.StatusOK, html)
	}
}
