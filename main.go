// Command pakka runs the Pakka diagnostic harness: a server-side Go service
// exposing bounded read-only diagnostic checks and model-guided
// investigations over HTTP, plus a Discord bot entry point. See README.md
// and docs/architecture.md.
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

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/discord"
	"pakka/internal/investigate"
	"pakka/internal/meta"
	"pakka/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("pakka failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg := config.Load()
	runner := diagnostics.NewRunner(cfg)
	investigator := investigate.NewInvestigator(cfg)
	var model investigate.Model
	var metaClient *meta.Client
	if cfg.MetaAPIKey != "" {
		metaClient = meta.NewClient(cfg.MetaAPIBase, cfg.MetaAPIKey, cfg.MetaModel, 60*time.Second)
		model = investigate.ClientModel{Client: metaClient}
		log.Info("meta integration enabled", "model", cfg.MetaModel)
	} else {
		log.Info("meta integration disabled: no API key configured")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var bot *discord.Bot
	if cfg.DiscordToken != "" {
		b, err := discord.New(cfg, runner, investigator, model, metaClient, log)
		if err != nil {
			return err
		}
		if err := b.Start(); err != nil {
			return fmt.Errorf("discord: %w", err)
		}
		bot = b
		defer bot.Stop()
		log.Info("discord connected")
		startMonitor(ctx, cfg, b, log)
		startWelcomer(ctx, cfg, b, log)
	} else {
		log.Info("discord disabled: no bot token configured")
	}
	var watcher *discord.LogWatcher
	if bot != nil && (cfg.LogModChannel != "" || cfg.LogOpsChannel != "") {
		watcher = discord.NewLogWatcher(cfg.LogWatchUnits,
			cfg.LogModChannel, cfg.LogOpsChannel,
			discord.SenderAnnouncer{Sender: bot.Session()}, 30*time.Minute, log)
		log.Info("log watcher started")
	} else {
		log.Info("log watcher disabled: needs bot plus alert channels")
	}
	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: server.New(server.Options{
			Runner:       runner,
			Investigator: investigator,
			Model:        model,
			LogWatcher:   watcher,
			MaxRuns:      cfg.MaxRuns,
			Log:          log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("pakka listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		log.Info("pakka shutting down")
		return srv.Shutdown(shutdown)
	case err := <-errCh:
		return err
	}
}

// startMonitor launches outage announcements when a status channel and at
// least one valid target are configured.
func startMonitor(ctx context.Context, cfg config.Config, b *discord.Bot, log *slog.Logger) {
	targets := discord.ParseTargets(cfg.StatusTargetsRaw)
	if cfg.DiscordStatusChannel == "" || len(targets) == 0 {
		log.Info("status monitor disabled: needs status channel and targets")
		return
	}
	mon := discord.NewMonitor(targets, cfg.StatusInterval,
		discord.NewNetProber(cfg.CheckTimeout),
		discord.SenderAnnouncer{Sender: b.Session()},
		cfg.DiscordStatusChannel, log)
	go mon.Run(ctx)
	log.Info("status monitor started", "targets", len(targets))
}

// startWelcomer launches newcomer welcomes when a welcome channel is set.
func startWelcomer(ctx context.Context, cfg config.Config, b *discord.Bot, log *slog.Logger) {
	if cfg.DiscordWelcomeChannel == "" {
		log.Info("welcomer disabled: no welcome channel configured")
		return
	}
	ch, err := b.Session().Channel(cfg.DiscordWelcomeChannel)
	if err != nil || ch.GuildID == "" {
		log.Warn("welcomer disabled: cannot resolve welcome channel", "err", err)
		return
	}
	w := discord.NewWelcomer(
		discord.NewGuildLister(b.Session(), ch.GuildID),
		b.Session(), cfg.DiscordWelcomeChannel,
		cfg.DiscordWelcomeInterval, cfg.DiscordPrefix, log)
	go w.Run(ctx)
	log.Info("welcomer started", "guild", ch.GuildID)
}
