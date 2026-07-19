package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// This migration creates the three application collections:
//
//   - admins: platform staff who validate/reject transactions, create
//     payment links on behalf of a merchant and manage merchant accounts.
//     Kept separate from PocketBase's own "_superusers" so staff never get
//     access to the PocketBase dashboard / raw schema.
//   - merchants: the integrating "accounts" (Stripe-style). Each merchant
//     can create payment links either via the /v1 API (using an api key)
//     or by logging into the small /app dashboard (using email+password,
//     since merchants is an auth collection).
//   - transactions: one payment link/attempt. See internal/txn for the
//     status state machine.
//
// All three collections are left with PocketBase's default (superuser-only)
// API rules: every access in this app goes through our own custom routes
// under internal/handlers, never through PocketBase's built-in REST API.
// timestampFields returns fresh "created"/"updated" autodate fields.
// Unlike older PocketBase versions these are not added automatically, so
// every collection that wants to be sortable/filterable by them (all of
// ours) must add them explicitly.
func timestampFields() []core.Field {
	return []core.Field{
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	}
}

func init() {
	m.Register(func(app core.App) error {
		admins := core.NewAuthCollection("admins")
		admins.Fields.Add(
			&core.TextField{Name: "name", Required: true, Max: 200},
		)
		admins.Fields.Add(timestampFields()...)
		if err := app.Save(admins); err != nil {
			return err
		}

		merchants := core.NewAuthCollection("merchants")
		merchants.Fields.Add(
			&core.TextField{Name: "name", Required: true, Max: 200},
			&core.TextField{Name: "api_key_hash", Required: true, Max: 64},
			&core.TextField{Name: "api_key_prefix", Required: true, Max: 20},
			&core.TextField{Name: "webhook_url", Max: 500},
			&core.TextField{Name: "webhook_secret", Max: 100},
			&core.SelectField{
				Name:      "status",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"active", "disabled"},
			},
		)
		merchants.Fields.Add(timestampFields()...)
		merchants.AddIndex("idx_merchants_api_key_hash", true, "api_key_hash", "")
		if err := app.Save(merchants); err != nil {
			return err
		}

		transactions := core.NewBaseCollection("transactions")
		transactions.Fields.Add(
			&core.RelationField{
				Name:         "merchant",
				Required:     true,
				CollectionId: merchants.Id,
				MaxSelect:    1,
			},
			&core.TextField{Name: "token", Required: true, Max: 60},
			&core.NumberField{Name: "amount", Required: true, Min: floatPtr(1)},
			&core.TextField{Name: "currency", Required: true, Max: 10},
			&core.TextField{Name: "description", Max: 500},
			&core.JSONField{Name: "metadata", MaxSize: 20000},
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
				Name:      "created_by",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"api", "merchant", "admin"},
			},
			&core.SelectField{
				Name:      "status",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"pending", "submitted", "validated", "rejected"},
			},
			&core.TextField{Name: "payer_msisdn", Max: 30},
			&core.TextField{Name: "payer_operator_ref", Max: 100},
			&core.DateField{Name: "submitted_at"},
			&core.TextField{Name: "resolved_by_email", Max: 200},
			&core.DateField{Name: "resolved_at"},
			&core.TextField{Name: "resolution_note", Max: 500},
			&core.TextField{Name: "success_url", Max: 500},
			&core.TextField{Name: "cancel_url", Max: 500},
			&core.BoolField{Name: "webhook_delivered"},
			&core.NumberField{Name: "webhook_attempts"},
		)
		transactions.Fields.Add(timestampFields()...)
		transactions.AddIndex("idx_transactions_token", true, "token", "")
		transactions.AddIndex("idx_transactions_merchant_status", false, "merchant, status", "")
		transactions.AddIndex("idx_transactions_payer_ref", false, "payer_operator_ref", "")
		if err := app.Save(transactions); err != nil {
			return err
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{"transactions", "merchants", "admins"} {
			collection, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				continue
			}
			if err := app.Delete(collection); err != nil {
				return err
			}
		}
		return nil
	})
}

func floatPtr(v float64) *float64 {
	return &v
}
