package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// payment_links turns a payment link into a reusable "product": created once
// by a merchant (or admin, or via the /v1 API), it can be paid by any number
// of different payers, each attempt recorded as its own row in transactions
// (see the payment_link relation added to transactions below). A link has
// its own lifecycle independent of any single payment:
//
//   - published:   visible and payable at /pay/{token}.
//   - unpublished: hidden from /pay/{token}, but reversible (can be
//     re-published).
//   - revoked:     hidden from /pay/{token}, terminal — see
//     internal/paylink.SetStatus for the one-way rule.
//
// amount/description/operator/mode are the "product" fields shown on the
// payment page; transactions copy them at creation time so the historical
// record stays accurate even if the link is edited later.
func init() {
	m.Register(func(app core.App) error {
		merchants, err := app.FindCollectionByNameOrId("merchants")
		if err != nil {
			return err
		}

		paymentLinks := core.NewBaseCollection("payment_links")
		paymentLinks.Fields.Add(
			&core.RelationField{
				Name:         "merchant",
				Required:     true,
				CollectionId: merchants.Id,
				MaxSelect:    1,
			},
			&core.TextField{Name: "token", Required: true, Max: 60},
			&core.TextField{Name: "title", Required: true, Max: 200},
			&core.TextField{Name: "description", Max: 1000},
			&core.JSONField{Name: "metadata", MaxSize: 20000},
			&core.FileField{
				Name:      "image",
				MaxSelect: 1,
				MaxSize:   5 * 1024 * 1024,
				MimeTypes: []string{"image/jpeg", "image/png", "image/webp"},
			},
			&core.NumberField{Name: "amount", Required: true, Min: floatPtr(1)},
			&core.TextField{Name: "currency", Required: true, Max: 10},
			&core.SelectField{
				Name:      "operator",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"airtel", "mtn"},
			},
			&core.SelectField{
				Name:      "mode",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"live", "test"},
			},
			&core.SelectField{
				Name:      "status",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"published", "unpublished", "revoked"},
			},
			&core.SelectField{
				Name:      "created_by",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"api", "merchant", "admin"},
			},
			&core.TextField{Name: "success_url", Max: 500},
			&core.TextField{Name: "cancel_url", Max: 500},
		)
		paymentLinks.Fields.Add(timestampFields()...)
		paymentLinks.AddIndex("idx_payment_links_token", true, "token", "")
		paymentLinks.AddIndex("idx_payment_links_merchant_status", false, "merchant, status", "")
		if err := app.Save(paymentLinks); err != nil {
			return err
		}

		transactions, err := app.FindCollectionByNameOrId("transactions")
		if err != nil {
			return err
		}

		for _, name := range []string{"token", "success_url", "cancel_url", "created_by"} {
			transactions.Fields.RemoveByName(name)
		}
		transactions.RemoveIndex("idx_transactions_token")

		transactions.Fields.Add(&core.RelationField{
			Name:         "payment_link",
			Required:     true,
			CollectionId: paymentLinks.Id,
			MaxSelect:    1,
		})
		transactions.AddIndex("idx_transactions_payment_link", false, "payment_link", "")

		return app.Save(transactions)
	}, func(app core.App) error {
		transactions, err := app.FindCollectionByNameOrId("transactions")
		if err == nil {
			transactions.Fields.RemoveByName("payment_link")
			transactions.RemoveIndex("idx_transactions_payment_link")

			transactions.Fields.Add(
				&core.TextField{Name: "token", Required: true, Max: 60},
				&core.TextField{Name: "success_url", Max: 500},
				&core.TextField{Name: "cancel_url", Max: 500},
				&core.SelectField{
					Name:      "created_by",
					Required:  true,
					MaxSelect: 1,
					Values:    []string{"api", "merchant", "admin"},
				},
			)
			transactions.AddIndex("idx_transactions_token", true, "token", "")
			if err := app.Save(transactions); err != nil {
				return err
			}
		}

		paymentLinks, err := app.FindCollectionByNameOrId("payment_links")
		if err != nil {
			return nil
		}
		return app.Delete(paymentLinks)
	})
}
