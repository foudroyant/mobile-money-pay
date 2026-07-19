package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"momopay/internal/payid"
	"momopay/internal/txn"
)

// registerAPIRoutes exposes the Stripe-style merchant REST API under /v1,
// authenticated with a per-merchant Bearer secret key.
func registerAPIRoutes(se *core.ServeEvent, app core.App) {
	g := se.Router.Group("/v1")
	g.BindFunc(requireAPIKey(app))

	g.POST("/transactions", apiCreateTransaction(app))
	g.GET("/transactions/{id}", apiGetTransaction(app))
	g.GET("/transactions", apiListTransactions(app))
}

type merchantCtx struct {
	Record *core.Record
	Live   bool
}

func requireAPIKey(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		const prefix = "Bearer "
		header := e.Request.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) {
			return e.UnauthorizedError("Missing API key. Send it as \"Authorization: Bearer sk_...\".", nil)
		}
		secret := strings.TrimPrefix(header, prefix)

		merchant, err := app.FindFirstRecordByFilter(
			"merchants",
			"api_key_hash = {:hash}",
			dbx.Params{"hash": payid.HashAPIKey(secret)},
		)
		if err != nil {
			return e.UnauthorizedError("Invalid API key.", nil)
		}
		if merchant.GetString("status") != "active" {
			return e.ForbiddenError("This merchant account is disabled.", nil)
		}

		e.Set("merchantCtx", &merchantCtx{Record: merchant, Live: payid.IsLiveKey(secret)})
		return e.Next()
	}
}

func currentMerchant(e *core.RequestEvent) *merchantCtx {
	ctx, _ := e.Get("merchantCtx").(*merchantCtx)
	return ctx
}

type createTransactionRequest struct {
	Amount      float64       `json:"amount" form:"amount"`
	Operator    string        `json:"operator" form:"operator"`
	Description string        `json:"description" form:"description"`
	SuccessURL  string        `json:"success_url" form:"success_url"`
	CancelURL   string        `json:"cancel_url" form:"cancel_url"`
	Metadata    types.JSONRaw `json:"metadata"`
}

func apiCreateTransaction(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		var body createTransactionRequest
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("Invalid request body.", err)
		}
		if body.Amount <= 0 {
			return e.BadRequestError("\"amount\" must be a positive number.", nil)
		}
		if body.Operator != "" && body.Operator != txn.OperatorAirtel {
			return e.BadRequestError(
				"MTN Mobile Money arrive bientôt. Utilisez \"operator\": \"airtel\" ou omettez ce champ.",
				nil,
			)
		}

		mode := txn.ModeTest
		if merchant.Live {
			mode = txn.ModeLive
		}

		record, err := txn.Create(app, txn.CreateParams{
			MerchantID:  merchant.Record.Id,
			Amount:      body.Amount,
			Description: body.Description,
			Metadata:    body.Metadata,
			SuccessURL:  body.SuccessURL,
			CancelURL:   body.CancelURL,
			Mode:        mode,
			CreatedBy:   txn.CreatedByAPI,
		})
		if err != nil {
			return e.InternalServerError("Failed to create transaction.", err)
		}

		return e.JSON(http.StatusCreated, transactionJSON(record, baseURL(e.Request)))
	}
}

func apiGetTransaction(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		record, err := app.FindRecordById("transactions", e.Request.PathValue("id"))
		if err != nil || record.GetString("merchant") != merchant.Record.Id {
			return e.NotFoundError("Transaction not found.", nil)
		}

		return e.JSON(http.StatusOK, transactionJSON(record, baseURL(e.Request)))
	}
}

func apiListTransactions(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		filter := "merchant = {:merchant}"
		params := dbx.Params{"merchant": merchant.Record.Id}

		if status := e.Request.URL.Query().Get("status"); status != "" {
			filter += " && status = {:status}"
			params["status"] = status
		}

		limit := 20
		if raw := e.Request.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}

		records, err := app.FindRecordsByFilter("transactions", filter, "-created", limit, 0, params)
		if err != nil {
			return e.InternalServerError("Failed to list transactions.", err)
		}

		base := baseURL(e.Request)
		out := make([]map[string]any, len(records))
		for i, r := range records {
			out[i] = transactionJSON(r, base)
		}

		return e.JSON(http.StatusOK, map[string]any{"data": out})
	}
}

func transactionJSON(r *core.Record, base string) map[string]any {
	return map[string]any{
		"id":          r.Id,
		"status":      r.GetString("status"),
		"amount":      r.GetFloat("amount"),
		"currency":    r.GetString("currency"),
		"operator":    r.GetString("operator"),
		"mode":        r.GetString("mode"),
		"description": r.GetString("description"),
		"payment_url": base + "/pay/" + r.GetString("token"),
		"created":     r.GetDateTime("created").String(),
	}
}

// baseURL resolves the public origin used to build payment_url links and
// webhook payloads. Set APP_BASE_URL in production (e.g. behind a reverse
// proxy) so it doesn't depend on the incoming Host header.
func baseURL(r *http.Request) string {
	if v := os.Getenv("APP_BASE_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}
