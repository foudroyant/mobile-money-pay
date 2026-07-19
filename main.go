package main

import (
	"log"
	"os"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/envfile"
	"momopay/internal/handlers"
	"momopay/internal/realtime"
	_ "momopay/migrations"
)

func main() {
	if err := envfile.Load(".env"); err != nil {
		log.Fatal(err)
	}

	app := pocketbase.New()

	hub := realtime.NewHub()
	handlers.BindRealtimeHooks(app, hub)
	handlers.BindReconcileHooks(app, hub)

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if err := e.App.RunAllMigrations(); err != nil {
			return err
		}
		if err := seedFirstAdmin(e.App); err != nil {
			return err
		}
		handlers.StartReconcileLoop(e.App, handlers.ReconcileInterval())
		return nil
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		handlers.Register(se, app, hub)
		return se.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// seedFirstAdmin creates the initial "admins" record from ADMIN_EMAIL /
// ADMIN_PASSWORD when the collection is still empty, since (unlike
// PocketBase's own _superusers) there is no "superuser upsert" CLI command
// for a custom auth collection.
func seedFirstAdmin(app core.App) error {
	email := os.Getenv("ADMIN_EMAIL")
	password := os.Getenv("ADMIN_PASSWORD")
	if email == "" || password == "" {
		return nil
	}

	existing, err := app.FindRecordsByFilter("admins", "", "", 1, 0)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}

	collection, err := app.FindCollectionByNameOrId("admins")
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Set("name", "Admin")
	record.Set("email", email)
	record.SetPassword(password)
	record.Set("verified", true)

	if err := app.Save(record); err != nil {
		return err
	}

	log.Printf("seeded initial admin account: %s", email)
	return nil
}
