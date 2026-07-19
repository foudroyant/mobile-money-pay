package handlers

import (
	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/realtime"
	"momopay/internal/txn"
	"momopay/internal/view"
)

// BindRealtimeHooks wires transaction create/update persistence into the
// SSE hub so the admin dashboard, the merchant dashboard and the public
// payment page all update live. It is intentionally separate from webhook
// dispatch (see internal/webhook), which is triggered explicitly from the
// admin validate/reject handler instead of from this generic hook — binding
// it here too would re-fire on the follow-up save that records delivery
// state and dispatch the webhook again in a loop.
func BindRealtimeHooks(app core.App, hub *realtime.Hub) {
	// rowPartial is "txn_row_new" (plain <tr>, prepended) on create or
	// "txn_row_update" (<tr hx-swap-oob="true">, patched in place by id)
	// on update — an OOB fragment can't create a row that doesn't exist yet
	// in a viewer's DOM, so new and updated rows need different wrappers
	// around the same shared cell markup.
	broadcast := func(e *core.RecordEvent, eventName, rowPartial string) error {
		record := e.Record

		if adminHTML, err := view.Partial(rowPartial, txnRowData(app, record, true)); err == nil {
			hub.Publish("admin", realtime.Event{Name: eventName, Data: adminHTML})
		}

		if merchantHTML, err := view.Partial(rowPartial, txnRowData(app, record, false)); err == nil {
			hub.Publish("merchant:"+record.GetString("merchant"), realtime.Event{Name: eventName, Data: merchantHTML})
		}

		if payHTML, err := view.Partial("pay_status", payStatusData(app, record)); err == nil {
			hub.Publish("txn:"+record.GetString("token"), realtime.Event{Name: "status", Data: payHTML})
		}

		return e.Next()
	}

	app.OnRecordAfterCreateSuccess(txn.CollectionName).BindFunc(func(e *core.RecordEvent) error {
		return broadcast(e, "txn-new", "txn_row_new")
	})

	app.OnRecordAfterUpdateSuccess(txn.CollectionName).BindFunc(func(e *core.RecordEvent) error {
		return broadcast(e, "txn-update", "txn_row_update")
	})
}

// BindReconcileHooks wires confirmed_refs create/update/delete into the SSE
// hub so the reconciliation page's pending list updates live — including
// when a match is found by the periodic background sweep rather than by
// the viewer's own action.
func BindReconcileHooks(app core.App, hub *realtime.Hub) {
	app.OnRecordAfterCreateSuccess(confirmedRefsCollection).BindFunc(func(e *core.RecordEvent) error {
		if html, err := view.Partial("reconcile_row_new", reconcileRowData(e.Record)); err == nil {
			hub.Publish("reconcile", realtime.Event{Name: "ref-new", Data: html})
		}
		return e.Next()
	})

	app.OnRecordAfterUpdateSuccess(confirmedRefsCollection).BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("status") == "matched" {
			if html, err := view.Partial("reconcile_row_removed", reconcileRowData(e.Record)); err == nil {
				hub.Publish("reconcile", realtime.Event{Name: "ref-removed", Data: html})
			}
		}
		return e.Next()
	})

	app.OnRecordAfterDeleteSuccess(confirmedRefsCollection).BindFunc(func(e *core.RecordEvent) error {
		if html, err := view.Partial("reconcile_row_removed", reconcileRowData(e.Record)); err == nil {
			hub.Publish("reconcile", realtime.Event{Name: "ref-removed", Data: html})
		}
		return e.Next()
	})
}
