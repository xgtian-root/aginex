package app

import (
	"errors"
	"testing"

	frameworkfiles "github.com/xgtian-root/aginex/server/framework/files"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/framework/observability"
	"github.com/xgtian-root/aginex/server/framework/services"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
)

func TestPublicFilesRuntimeFollowsModuleComposition(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			cfg := moduleTestConfig(t, config.Bootstrap{})
			db, err := database.Open(cfg.Database)
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			var modules []module.Module
			if enabled {
				modules = append(modules, FilesModule())
			}
			if err := MigrateUp(t.Context(), sqlDB, cfg, modules...); err != nil {
				t.Fatal(err)
			}
			recorder, err := observability.NewRecorder(nil)
			if err != nil {
				t.Fatal(err)
			}
			api, err := NewCompositionWithModulesAndObservability(cfg, db, recorder, modules...)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := services.ContextWithRuntime(t.Context(), api.services)
			if err != nil {
				t.Fatal(err)
			}
			runtime, ok := services.RuntimeFromContext(ctx)
			if !ok || (runtime.Files != nil) != enabled || runtime.Jobs != nil {
				t.Fatalf("runtime Files enabled = %t, Jobs = %v, resolved = %t", runtime.Files != nil, runtime.Jobs, ok)
			}
			for _, table := range []string{"file_objects", "file_reference_owners", "file_references"} {
				if db.Migrator().HasTable(table) != enabled {
					t.Fatalf("table %s does not follow FilesModule", table)
				}
			}
			if enabled {
				_, err := runtime.Files.GetReady(ctx, "00000000-0000-4000-8000-000000000001")
				if !errors.Is(err, frameworkfiles.ErrNotFound) {
					t.Fatalf("GetReady = %v, want public ErrNotFound", err)
				}
			}
		})
	}
}
