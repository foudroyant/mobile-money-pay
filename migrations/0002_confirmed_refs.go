package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// confirmed_refs backs the reconciliation tool: an admin pastes operator
// (Airtel Money) transaction IDs they've confirmed on their own operator
// dashboard/SMS. Each is matched — immediately on add, and again on every
// periodic sweep (see internal/handlers/reconcile.go) — against a
// "submitted" transaction sharing the same payer_operator_ref, and
// auto-validated on match. The periodic sweep exists because the operator
// confirmation can arrive before the payer has actually submitted their
// reference on the payment page.
func init() {
	m.Register(func(app core.App) error {
		collection := core.NewBaseCollection("confirmed_refs")
		collection.Fields.Add(
			&core.TextField{Name: "operator_ref", Required: true, Max: 100},
			&core.SelectField{
				Name:      "status",
				Required:  true,
				MaxSelect: 1,
				Values:    []string{"pending", "matched"},
			},
			&core.RelationField{
				Name:         "matched_transaction",
				CollectionId: "", // resolved below once the transactions collection is looked up
				MaxSelect:    1,
			},
			&core.TextField{Name: "added_by_email", Required: true, Max: 200},
		)
		collection.Fields.Add(timestampFields()...)

		transactions, err := app.FindCollectionByNameOrId("transactions")
		if err != nil {
			return err
		}
		if relField, ok := collection.Fields.GetByName("matched_transaction").(*core.RelationField); ok {
			relField.CollectionId = transactions.Id
		}

		collection.AddIndex("idx_confirmed_refs_status", false, "status", "")
		collection.AddIndex("idx_confirmed_refs_operator_ref", false, "operator_ref", "")

		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("confirmed_refs")
		if err != nil {
			return nil
		}
		return app.Delete(collection)
	})
}
