package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"time"

	alertmanager "github.com/metalmatze/alertmanager-bot/pkg/alertmanager"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/hako/durafmt"
	"github.com/joho/godotenv"
	"github.com/metalmatze/alertmanager-bot/pkg/telegram"
	"github.com/oklog/run"
	"github.com/prometheus/alertmanager/template"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
)

var (
	// Version of alertmanager-bot.
	Version string
	// Revision or Commit this binary was built from.
	Revision string
	// BuildDate this binary was built.
	BuildDate string
	// GoVersion running this binary.
	GoVersion = runtime.Version()
	// StartTime has the time this was started.
	StartTime = time.Now()
)

func main() {
	godotenv.Load()

	config := struct {
		alertmanager   *url.URL
		boltPath       string
		listenAddr     string
		logLevel       string
		logJSON        bool
		telegramAdmin  int
		telegramToken  string
		templatesPaths []string
	}{}

	a := kingpin.New("alertmanager-bot", "Bot for Prometheus' Alertmanager")
	a.HelpFlag.Short('h')

	a.Flag("alertmanager.url", "The URL that's used to connect to the alertmanager").
		Default("http://localhost:9093/").
		URLVar(&config.alertmanager)

	a.Flag("bolt.path", "The path to the file where bolt persists its data").
		Default("/tmp/bot.db").
		StringVar(&config.boltPath)

	a.Flag("listen.addr", "The address the alertmanager-bot listens on for incoming webhooks").
		Default("0.0.0.0:8080").
		StringVar(&config.listenAddr)

	a.Flag("log.json", "Tell the application to log json and not key value pairs").
		BoolVar(&config.logJSON)

	a.Flag("log.level", "The log level to use for filtering logs").
		Default(levelInfo).
		EnumVar(&config.logLevel, levelError, levelWarn, levelInfo, levelDebug)

	a.Flag("telegram.admin", "The ID of the initial Telegram Admin").
		Required().
		IntVar(&config.telegramAdmin)

	a.Flag("telegram.token", "The token used to connect with Telegram").
		Required().
		Envar("TELEGRAM_TOKEN").
		StringVar(&config.telegramToken)

	a.Flag("template.paths", "The paths to the template").
		Default("/templates/default.tmpl").
		ExistingFilesVar(&config.templatesPaths)

	_, err := a.Parse(os.Args[1:])
	if err != nil {
		fmt.Printf("error parsing commandline arguments: %v\n", err)
		a.Usage(os.Args[1:])
		os.Exit(2)
	}

	levelFilter := map[string]level.Option{
		levelError: level.AllowError(),
		levelWarn:  level.AllowWarn(),
		levelInfo:  level.AllowInfo(),
		levelDebug: level.AllowDebug(),
	}

	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	if config.logJSON {
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	}

	logger = level.NewFilter(logger, levelFilter[config.logLevel])
	logger = log.With(logger,
		"ts", log.DefaultTimestampUTC,
		"caller", log.DefaultCaller,
	)

	var tmpl *template.Template
	{
		funcs := template.DefaultFuncs
		funcs["since"] = func(t time.Time) string {
			return durafmt.Parse(time.Since(t)).String()
		}
		funcs["duration"] = func(start time.Time, end time.Time) string {
			return durafmt.Parse(end.Sub(start)).String()
		}

		template.DefaultFuncs = funcs
		tmpl, err = template.FromGlobs(config.templatesPaths...)
		if err != nil {
			level.Error(logger).Log("msg", "failed to parse templates", "err", err)
			os.Exit(1)
		}
		tmpl.ExternalURL = config.alertmanager
	}

	//var kvStore store.Store
	//kvStore, err = boltdb.New([]string{config.boltPath}, &store.Config{Bucket: "alertmanager"})
	//if err != nil {
	//	level.Error(logger).Log("msg", "failed to create bolt store backend", "err", err)
	//	os.Exit(1)
	//}
	//defer kvStore.Close()

	_, cancel := context.WithCancel(context.Background())

	//// TODO Needs fan out for multiple bots
	//webhooks := make(chan notify.WebhookMessage, 32)

	am := alertmanager.New(config.alertmanager)

	var g run.Group
	{
		bot, err := telegram.NewBot(
			log.WithPrefix(logger, "component", "telegram"),
			am,
			tmpl,
			config.telegramToken,
		)
		if err != nil {
			level.Error(logger).Log("msg", "failed to create Telegram bot", "err", err)
			os.Exit(1)
		}

		g.Add(func() error {
			bot.Run()
			return nil
		}, func(err error) {
			bot.Shutdown()
		})
	}
	//{
	//	tlogger := log.With(logger, "component", "telegram")
	//
	//	chats, err := telegram.NewChatStore(kvStore)
	//	if err != nil {
	//		level.Error(logger).Log("msg", "failed to create chat store", "err", err)
	//		os.Exit(1)
	//	}
	//
	//	bot, err := telegram.NewBot(
	//		chats, config.telegramToken, config.telegramAdmin,
	//		telegram.WithLogger(tlogger),
	//		telegram.WithAddr(config.listenAddr),
	//		telegram.WithAlertmanager(config.alertmanager),
	//		telegram.WithTemplates(tmpl),
	//		telegram.WithRevision(Revision),
	//		telegram.WithStartTime(StartTime),
	//	)
	//	if err != nil {
	//		level.Error(tlogger).Log("msg", "failed to create bot", "err", err)
	//		os.Exit(2)
	//	}
	//
	//	g.Add(func() error {
	//		level.Info(tlogger).Log(
	//			"msg", "starting alertmanager-bot",
	//			"version", Version,
	//			"revision", Revision,
	//			"buildDate", BuildDate,
	//			"goVersion", GoVersion,
	//		)
	//
	//		// Runs the bot itself communicating with Telegram
	//		return bot.Run(ctx, webhooks)
	//	}, func(err error) {
	//		cancel()
	//	})
	//}
	//{
	//	wlogger := log.With(logger, "component", "webserver")
	//
	//	// TODO: Use Heptio's healthcheck library
	//	handleHealth := func(w http.ResponseWriter, r *http.Request) {
	//		w.WriteHeader(http.StatusOK)
	//	}
	//
	//	webhooksCounter := prometheus.NewCounter(prometheus.CounterOpts{
	//		Namespace: "alertmanagerbot",
	//		Name:      "webhooks_total",
	//		Help:      "Number of webhooks received by this bot",
	//	})
	//
	//	prometheus.MustRegister(webhooksCounter)
	//
	//	m := http.NewServeMux()
	//	//m.HandleFunc("/", alertmanager.HandleWebhook(wlogger, webhooksCounter, webhooks))
	//	m.Handle("/metrics", promhttp.Handler())
	//	m.HandleFunc("/health", handleHealth)
	//	m.HandleFunc("/healthz", handleHealth)
	//
	//	s := http.Server{
	//		Addr:    config.listenAddr,
	//		Handler: m,
	//	}
	//
	//	g.Add(func() error {
	//		level.Info(wlogger).Log("msg", "starting webserver", "addr", config.listenAddr)
	//		return s.ListenAndServe()
	//	}, func(err error) {
	//		s.Shutdown(context.Background())
	//	})
	//}
	{
		sig := make(chan os.Signal)
		signal.Notify(sig, os.Interrupt, os.Kill)

		g.Add(func() error {
			<-sig
			return nil
		}, func(err error) {
			cancel()
			close(sig)
		})
	}

	if err := g.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
