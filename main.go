// Command pakka runs the Pakka diagnostic harness: a server-side Go service
// exposing bounded read-only diagnostic checks and model-guided
// investigations over HTTP, plus a Discord bot entry point. See README.md
// and docs/architecture.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pakka/internal/chat"
	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/discord"
	"pakka/internal/eshop"
	"pakka/internal/investigate"
	"pakka/internal/meta"
	"pakka/internal/publisher"
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
		if cfg.DiscordGateway {
			if err := b.Start(); err != nil {
				return fmt.Errorf("discord: %w", err)
			}
			defer b.Stop()
			log.Info("discord connected")
		} else {
			log.Info("discord gateway disabled: REST-only alerts mode")
		}
		bot = b
		startMonitor(ctx, cfg, b, log)
		startWelcomer(ctx, cfg, b, log)
		startEshop(ctx, cfg, b, log)
	} else {
		log.Info("discord disabled: no bot token configured")
	}
	var watcher *discord.LogWatcher
	if bot != nil && (cfg.LogModChannel != "" || cfg.LogOpsChannel != "") {
		watcher = discord.NewLogWatcher(cfg.LogWatchUnits,
			cfg.LogModChannel, cfg.LogOpsChannel,
			discord.SenderAnnouncer{Sender: bot.RedactingSender()}, 30*time.Minute, log)
		bot.SetAgentWatcher(watcher)
		log.Info("log watcher started")
	} else {
		log.Info("log watcher disabled: needs bot plus alert channels")
	}
	pub, writer := startPublisher(ctx, cfg, bot, metaClient, log)
	chatSvc := startChat(ctx, cfg, investigator, model, log)
	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: server.New(server.Options{
			Runner:       runner,
			Investigator: investigator,
			Model:        model,
			LogWatcher:   watcher,
			Publisher:    pub,
			Writer:       writer,
			Chat:         chatSvc,
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
		discord.SenderAnnouncer{Sender: b.RedactingSender()},
		cfg.DiscordStatusChannel, log)
	if cfg.AlertsChannel != "" {
		mon.SetAlertsMirror(discord.SenderAnnouncer{Sender: b.RedactingSender()}, cfg.AlertsChannel)
	}
	b.SetAgentMonitor(mon)
	go mon.Run(ctx)
	log.Info("status monitor started", "targets", len(targets))
}

// startEshop launches eShop update monitoring when the store API and both
// announce channels are configured. Run on one host only: every instance
// would otherwise post the same announcements.
func startEshop(ctx context.Context, cfg config.Config, b *discord.Bot, log *slog.Logger) {
	if cfg.EshopAPIBase == "" || cfg.AlertsChannel == "" || cfg.EshopAnnounceChannel == "" {
		log.Info("eshop watcher disabled: needs api base plus staff and announce channels")
		return
	}
	sources, err := eshop.ParseSources(cfg.EshopSources)
	if err != nil {
		log.Warn("eshop watcher: bad sources, catalog tracking only", "err", err)
		sources = map[string]string{}
	}
	var trigger eshop.Trigger
	if cfg.EshopSyncCommand != "" {
		trigger = eshop.NewCommandTrigger(cfg.EshopSyncCommand, cfg.EshopSyncTimeout, cfg.EshopSyncCooldown)
	} else {
		log.Info("eshop watcher: no sync command, detect and announce only")
	}
	// The catalog list is megabytes; give both clients room regardless of
	// the fast diagnostics timeout.
	timeout := max(cfg.CheckTimeout, 30*time.Second)
	w := eshop.New(
		eshop.NewStoreClient(cfg.EshopAPIBase, cfg.EshopInternalKey, timeout),
		eshop.NewGitHubClient(cfg.GitHubToken, timeout),
		trigger,
		discord.SenderAnnouncer{Sender: b.RedactingSender()},
		discord.SenderAnnouncer{Sender: b.RedactingSender()},
		cfg.AlertsChannel, cfg.EshopAnnounceChannel,
		sources, log)
	b.SetEshopWatcher(w)
	go w.Run(ctx, cfg.EshopInterval)
	log.Info("eshop watcher started", "sources", len(sources))
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
		b.RedactingSender(), cfg.DiscordWelcomeChannel,
		cfg.DiscordWelcomeInterval, cfg.DiscordPrefix, log)
	go w.Run(ctx)
	log.Info("welcomer started", "guild", ch.GuildID)
}

// metaWriter lets the publisher use the Meta model for drafts. Its output is
// only ever queued for staff review.
type metaWriter struct{ c *meta.Client }

func (w metaWriter) Write(ctx context.Context, system, user string) (string, error) {
	reply, err := w.c.Chat(ctx, []meta.Message{
		{Role: meta.RoleSystem, Content: system},
		{Role: meta.RoleUser, Content: user},
	}, nil)
	return reply.Content, err
}

// startPublisher builds the publisher when OpenVerse is configured: the
// official-account posts, the News mirror, the review queue and the staff
// commands. Run on one host only, like the eShop watcher.
func startPublisher(ctx context.Context, cfg config.Config, b *discord.Bot, mc *meta.Client, log *slog.Logger) (*publisher.Service, publisher.Writer) {
	pcfg := publisher.LoadConfig(os.Getenv)
	if !pcfg.Enabled() {
		log.Info("publisher disabled: needs PAKKA_OPENVERSE_URL and PAKKA_OPENVERSE_KEY")
		return nil, nil
	}
	var ov publisher.OpenVerse = publisher.NewOpenVerse(pcfg.OpenVerseURL, pcfg.OpenVerseKey, 0)
	var news publisher.NewsAdmin
	if pcfg.NewsBase != "" {
		news = publisher.NewNews(pcfg.NewsBase, pcfg.NewsKey, 0)
	}
	svc, err := publisher.New(pcfg, ov, news, log)
	if err != nil {
		log.Error("publisher disabled: cannot start", "err", err)
		return nil, nil
	}
	if b != nil && cfg.AlertsChannel != "" {
		ann := discord.SenderAnnouncer{Sender: b.RedactingSender()}
		svc.Notify = func(msg string) {
			if err := ann.Announce(cfg.AlertsChannel, msg); err != nil {
				log.Warn("publisher staff notice failed", "err", err)
			}
		}
	}
	if b != nil {
		b.SetPublisher(svc)
	}
	// The highlights writer: the process model, or its own credentials when
	// PAKKA_PUBLISH_META_API_KEY is set (so the publisher can be given a model
	// without switching the Discord features on). Its text is only ever a draft.
	var w publisher.Writer
	if mc != nil {
		w = metaWriter{mc}
	}
	if key := os.Getenv("PAKKA_PUBLISH_META_API_KEY"); key != "" {
		id := os.Getenv("PAKKA_PUBLISH_META_MODEL")
		if id == "" {
			id = cfg.MetaModel
		}
		w = metaWriter{meta.NewClient(cfg.MetaAPIBase, key, id, 60*time.Second)}
	}
	if news != nil {
		go svc.RunMirror(ctx, pcfg.MirrorInterval)
	}
	go svc.RunHighlights(ctx, w, pcfg.HighlightsEvery)
	cats := make([]string, 0, len(pcfg.Categories))
	for c := range pcfg.Categories {
		cats = append(cats, c)
	}
	log.Info("publisher started", "killed", svc.Killed(), "categories", cats, "news", news != nil, "admin_api", pcfg.AdminKey != "")
	return svc, w
}

// startChat starts the player chat service when PAKKA_CHAT_KEY is set. It
// shares this one Pakka process; there is no second service.
//
// The chat model is the process model unless PAKKA_CHAT_META_API_KEY is set, which gives chat
// its own Meta credentials (and PAKKA_CHAT_META_MODEL its own model id), so enabling chat does
// not switch the Discord and publisher features on or change their model.
func startChat(ctx context.Context, cfg config.Config, inv *investigate.Investigator, model investigate.Model, log *slog.Logger) *chat.Service {
	ccfg := chat.LoadConfig(os.Getenv("PAKKA_STATE_DIR"))
	if ccfg.Key == "" {
		log.Info("player chat disabled: no PAKKA_CHAT_KEY")
		return nil
	}
	if key := os.Getenv("PAKKA_CHAT_META_API_KEY"); key != "" {
		id := os.Getenv("PAKKA_CHAT_META_MODEL")
		if id == "" {
			id = cfg.MetaModel
		}
		model = investigate.ClientModel{Client: meta.NewClient(cfg.MetaAPIBase, key, id, 60*time.Second)}
		log.Info("player chat uses its own model credentials", "model", id)
	}
	agent := &chat.Agent{
		Model:   model,
		Docs:    chat.NewDocs(ccfg.DocsIndexURL, ccfg.DocsBaseURL),
		Website: chat.NewWebsite(ccfg.WebsiteURL, ccfg.Key),
	}
	if model != nil {
		agent.Diagnose = func(ctx context.Context, symptoms string) (string, error) {
			f, results, err := inv.Run(ctx, model, chat.Sanitize(symptoms))
			if err != nil {
				return "", err
			}
			type row struct{ Check, Outcome, Summary string }
			rows := []row{}
			for _, r := range results {
				rows = append(rows, row{r.CheckID, string(r.Outcome), r.Summary})
			}
			b, _ := json.Marshal(map[string]any{"finding": f, "checks": rows})
			return string(b), nil
		}
	}
	svc, err := chat.New(ccfg, agent, log)
	if err != nil {
		log.Error("player chat disabled", "err", err)
		return nil
	}
	go svc.Run(ctx)
	log.Info("player chat enabled", "model_configured", model != nil)
	return svc
}
