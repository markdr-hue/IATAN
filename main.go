/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package main

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/markdr-hue/IATAN/brain"
	"github.com/markdr-hue/IATAN/caddy"
	"github.com/markdr-hue/IATAN/chat"
	"github.com/markdr-hue/IATAN/config"
	"github.com/markdr-hue/IATAN/db"
	"github.com/markdr-hue/IATAN/db/models"
	"github.com/markdr-hue/IATAN/events"
	"github.com/markdr-hue/IATAN/llm"
	"github.com/markdr-hue/IATAN/llm/anthropic"
	"github.com/markdr-hue/IATAN/llm/openai"
	"github.com/markdr-hue/IATAN/security"
	"github.com/markdr-hue/IATAN/server"
	"github.com/markdr-hue/IATAN/tools"
	"github.com/markdr-hue/IATAN/webhooks"
)

var Version = "0.2.0"

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	initLogger(cfg.LogLevel)

	slog.Info("IATAN_GO starting",
		"version", Version,
		"admin_port", cfg.AdminPort,
		"public_port", cfg.PublicPort,
	)

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	encryptor, err := security.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		slog.Error("failed to init encryptor", "error", err)
		os.Exit(1)
	}

	jwtMgr := security.NewJWTManager(cfg.JWTSecret, 24*time.Hour)
	jwtMgr.SecureCookies = cfg.CaddyEnabled // Caddy implies TLS

	siteDBMgr := db.NewSiteDBManager(cfg.DataDir)
	defer siteDBMgr.CloseAll()

	bus := events.NewBus()
	_ = webhooks.NewDispatcher(siteDBMgr, bus)

	llmRegistry := llm.NewRegistry()
	toolRegistry := tools.NewRegistry()
	tools.RegisterAll(toolRegistry)
	toolExecutor := tools.NewExecutor(toolRegistry)

	slog.Info("tools registered", "count", len(toolRegistry.List()))

	llmHTTPClient := &http.Client{Timeout: cfg.LLMTimeout()}
	providerFactory := func(name, providerType, apiKey, baseURL string) llm.Provider {
		switch strings.ToLower(providerType) {
		case "anthropic":
			opts := []anthropic.Option{anthropic.WithHTTPClient(llmHTTPClient)}
			if baseURL != "" {
				opts = append(opts, anthropic.WithBaseURL(baseURL))
			}
			return anthropic.New(name, apiKey, opts...)
		case "openai":
			opts := []openai.Option{openai.WithHTTPClient(llmHTTPClient)}
			if baseURL != "" {
				opts = append(opts, openai.WithBaseURL(baseURL))
			}
			return openai.New(name, apiKey, opts...)
		default:
			return nil
		}
	}

	if err := llm.LoadFirstRunWithFactory(cfg.FirstRunPath, database.DB, encryptor, llmRegistry, providerFactory); err != nil {
		slog.Debug("firstrun seed skipped", "reason", err)
	}

	brainDeps := &brain.Deps{
		DB:              database.DB,
		SiteDBManager:   siteDBMgr,
		Encryptor:       encryptor,
		LLMRegistry:     llmRegistry,
		ToolRegistry:    toolRegistry,
		ToolExecutor:    toolExecutor,
		Bus:             bus,
		ProviderFactory: providerFactory,
		MonitoringBase:  time.Duration(cfg.BrainMonitoringBaseSec) * time.Second,
		MonitoringMax:   time.Duration(cfg.BrainMonitoringMaxSec) * time.Second,
	}
	brainCtx, brainCancel := context.WithCancel(context.Background())
	defer brainCancel()
	brainMgr := brain.NewBrainManager(brainDeps, brainCtx)

	// Auto-start brain when a new site is created (via any path)
	bus.Subscribe(events.EventSiteCreated, func(e events.Event) {
		if err := brainMgr.StartSite(e.SiteID); err != nil {
			slog.Error("failed to auto-start brain on site creation", "site_id", e.SiteID, "error", err)
		}
	})

	// Wake the brain when a question is answered so it can resume building.
	// Pass the answer context so the brain can acknowledge it.
	bus.Subscribe(events.EventQuestionAnswered, func(e events.Event) {
		cmd := brain.BrainCommand{
			Type: brain.CommandWake,
			Payload: map[string]interface{}{
				"reason":      "question_answered",
				"question_id": e.Payload["question_id"],
				"answer":      e.Payload["answer"],
			},
		}
		if err := brainMgr.SendCommand(e.SiteID, cmd); err != nil {
			slog.Error("failed to wake brain after question answered, retrying", "site_id", e.SiteID, "error", err)
			time.AfterFunc(3*time.Second, func() {
				if err2 := brainMgr.SendCommand(e.SiteID, cmd); err2 != nil {
					slog.Error("retry wake after question answered also failed", "site_id", e.SiteID, "error", err2)
				}
			})
		}
	})

	// Wake the brain when the user sends a chat message so it can
	// validate any changes the chat may have made, or fix things during monitoring.
	bus.Subscribe(events.EventChatMessage, func(e events.Event) {
		role, _ := e.Payload["role"].(string)
		if role != "user" {
			return
		}
		content, _ := e.Payload["content"].(string)
		_ = brainMgr.SendCommand(e.SiteID, brain.BrainCommand{
			Type:    brain.CommandChat,
			Payload: map[string]interface{}{"message": content},
		})
	})

	chatDeps := chat.SessionDeps{
		DB:            database.DB,
		SiteDBManager: siteDBMgr,
		LLMRegistry:   llmRegistry,
		ToolRegistry:  toolRegistry,
		ToolExecutor:  toolExecutor,
		Bus:           bus,
		Logger:        slog.Default().With("component", "chat"),
		Encryptor:     encryptor,
	}
	chatHandler := chat.NewChatHandler(chatDeps)

	userCount, err := models.CountUsers(database.DB)
	if err != nil {
		slog.Error("failed to count users", "error", err)
		os.Exit(1)
	}

	if userCount == 0 {
		slog.Info("first run detected, setup required via admin UI")
	} else {
		slog.Info("existing installation detected", "users", userCount)
	}

	if err := brainMgr.AutoStart(); err != nil {
		slog.Error("brain auto-start failed", "error", err)
	}

	brainMgr.StartScheduler()
	brainMgr.StartLogCleanup()

	caddyMgr := caddy.NewManager(cfg, database.DB, slog.Default())
	caddyMgr.SubscribeToEvents(bus)
	if err := caddyMgr.Start(); err != nil {
		slog.Error("caddy start failed", "error", err)
	}

	adminSubFS, err := fs.Sub(adminFS, "web/admin")
	if err != nil {
		slog.Error("failed to create admin sub-filesystem", "error", err)
		os.Exit(1)
	}

	srv := server.New(&server.ServerDeps{
		Config:          cfg,
		DB:              database,
		SiteDBManager:   siteDBMgr,
		JWTManager:      jwtMgr,
		Encryptor:       encryptor,
		Bus:             bus,
		BrainManager:    brainMgr,
		ChatHandler:     chatHandler,
		LLMRegistry:     llmRegistry,
		ToolRegistry:    toolRegistry,
		ProviderFactory: providerFactory,
		Logger:          slog.Default(),
		AdminFS:         adminSubFS,
		Version:         Version,
	})

	slog.Info("IATAN_GO ready",
		"admin", cfg.AdminPort,
		"public", cfg.PublicPort,
		"caddy", cfg.CaddyEnabled,
	)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		slog.Error("server error", "error", err)
	}

	// Graceful shutdown
	caddyMgr.Stop()
	brainMgr.StopAll()
	slog.Info("IATAN_GO shutdown complete")
}

func initLogger(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))
}
