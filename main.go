package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/1azunna/dependency-track-exporter/internal/exporter"
	dtrack "github.com/DependencyTrack/client-go"
	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"
)

const (
	envAddress string = "DEPENDENCY_TRACK_ADDR"
	envAPIKey  string = "DEPENDENCY_TRACK_API_KEY"
)

func init() {
	prometheus.MustRegister(collectors.NewBuildInfoCollector())
}

func main() {
	var (
		webConfig                    = webflag.AddFlags(kingpin.CommandLine, ":9916")
		metricsPath                  = kingpin.Flag("web.metrics-path", "Path under which to expose metrics").Default("/metrics").String()
		dtAddress                    = kingpin.Flag("dtrack.address", fmt.Sprintf("Dependency-Track server address (can also be set with $%s)", envAddress)).Default("http://localhost:8080").Envar(envAddress).String()
		dtAPIKey                     = kingpin.Flag("dtrack.api-key", fmt.Sprintf("Dependency-Track API key (can also be set with $%s)", envAPIKey)).Envar(envAPIKey).Required().String()
		dtProjectTags                = kingpin.Flag("dtrack.project-tags", "Comma-separated list of project tags to filter on").String()
		pollInterval                 = kingpin.Flag("dtrack.poll-interval", "Interval to poll Dependency-Track for metrics").Default("6h").Duration()
		dtInitializeViolationMetrics = kingpin.Flag("dtrack.initialize-violation-metrics", "Initialize all possible violation metric combinations to 0").Default("true").String()
		promslogConfig               = promslog.Config{}
	)

	flag.AddFlags(kingpin.CommandLine, &promslogConfig)
	kingpin.Version(version.Print(exporter.Namespace + "_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	logger := promslog.New(&promslogConfig)

	logger.Info("Starting exporter", "namespace", exporter.Namespace, "version", version.Info(), "build_context", version.BuildContext())

	c, err := dtrack.NewClient(*dtAddress, dtrack.WithAPIKey(*dtAPIKey))
	if err != nil {
		logger.Error("Error creating client", "err", err)
		os.Exit(1)
	}

	var projectTags []string
	if *dtProjectTags != "" {
		projectTags = strings.Split(*dtProjectTags, ",")
	}

	initViolationMetrics, err := strconv.ParseBool(*dtInitializeViolationMetrics)
	if err != nil {
		logger.Error("Error parsing dtrack.initialize-violation-metrics", "err", err)
		os.Exit(1)
	}

	e := exporter.Exporter{
		Client:                     c,
		Logger:                     logger,
		ProjectTags:                projectTags,
		InitializeViolationMetrics: initViolationMetrics,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Run(ctx, *pollInterval)

	http.HandleFunc(*metricsPath, e.HandlerFunc())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>
						 <head><title>Dependency-Track Exporter</title></head>
						 <body>
						 <h1>Dependency-Track Exporter</h1>
						 <p><a href='` + *metricsPath + `'>Metrics</a></p>
						 </body>
						 </html>`))
	})

	srvc := make(chan struct{})
	term := make(chan os.Signal, 1)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM)

	go func() {
		srv := &http.Server{}
		if err := web.ListenAndServe(srv, webConfig, logger); err != http.ErrServerClosed {
			logger.Error("Error starting HTTP server", "err", err)
			close(srvc)
		}
	}()

	for {
		select {
		case <-term:
			logger.Info("Received SIGTERM, exiting gracefully...")
			os.Exit(0)
		case <-srvc:
			os.Exit(1)
		}
	}
}
