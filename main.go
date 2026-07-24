package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SokvisalMong/rice/internal/bot"
	"github.com/SokvisalMong/rice/internal/config"
	"github.com/SokvisalMong/rice/internal/ggdeals"
	"github.com/SokvisalMong/rice/internal/steam"
	"github.com/SokvisalMong/rice/internal/store"
	"github.com/bwmarrin/discordgo"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("Rice stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStartup()
	database, err := store.Open(startupCtx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()

	httpClient := &http.Client{Timeout: 12 * time.Second}
	steamClient := steam.NewClient(httpClient)
	dealsClient := ggdeals.NewCachedAPI(ggdeals.NewClient(cfg.GGDealsAPIKey, httpClient), 5*time.Minute)
	handler := bot.NewHandler(steamClient, dealsClient, database, logger)
	defer handler.Close()

	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return fmt.Errorf("create Discord session: %w", err)
	}
	session.Client.Timeout = 15 * time.Second
	session.Identify.Intents = discordgo.IntentsGuilds
	session.AddHandler(handler.HandleInteraction)
	session.AddHandler(func(_ *discordgo.Session, ready *discordgo.Ready) {
		logger.Info("Discord gateway ready", "bot", ready.User.Username, "guilds", len(ready.Guilds))
	})

	if err := session.Open(); err != nil {
		return fmt.Errorf("open Discord gateway: %w", err)
	}
	defer session.Close()
	if session.State == nil || session.State.User == nil || session.State.User.ID == "" {
		return errors.New("Discord READY event did not include the bot application ID")
	}

	registered, err := session.ApplicationCommandBulkOverwrite(session.State.User.ID, cfg.TestGuildID, handler.Commands())
	if err != nil {
		return fmt.Errorf("register Discord application commands: %w", err)
	}
	scope := "global"
	if cfg.TestGuildID != "" {
		scope = "guild " + cfg.TestGuildID
	}
	logger.Info("Rice is running", "command_scope", scope, "commands", len(registered))

	<-ctx.Done()
	logger.Info("Shutting down Rice")
	return nil
}
