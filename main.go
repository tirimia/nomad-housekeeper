package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/araddon/dateparse"
	"github.com/gin-gonic/gin"
	nomad_api "github.com/hashicorp/nomad/api"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"
	str2duration "github.com/xhit/go-str2duration/v2"
)

type Housekeeper struct {
	NomadClient *nomad_api.Client
}

type Config struct {
	Interval time.Duration `default:"30s"`
	DryRun   bool          `envconfig:"dry_run"`
	RunOnce  bool          `envconfig:"once"`
	Debug    bool          `envconfig:"debug"`
}

var (
	housekeeper Housekeeper
	config      Config
)

const (
	HousekeeperTTL     = "housekeeper/ttl"
	HousekeeperExpires = "housekeeper/expires"
	HousekeeperPurge   = "housekeeper/purge"
)

func init() {
	log.SetOutput(io.Discard) // Send all logs to nowhere by default

	log.AddHook(&writer.Hook{ // Send logs with level higher than warning to stderr
		Writer: os.Stderr,
		LogLevels: []log.Level{
			log.PanicLevel,
			log.FatalLevel,
			log.ErrorLevel,
			log.WarnLevel,
		},
	})
	log.AddHook(&writer.Hook{ // Send info and debug logs to stdout
		Writer: os.Stdout,
		LogLevels: []log.Level{
			log.InfoLevel,
			log.DebugLevel,
		},
	})

	log.SetFormatter(&log.JSONFormatter{})

	// DefaultConfig gets the NOMAD_ADDR and NOMAD_TOKEN env variables itself
	nomadClientConfig := nomad_api.DefaultConfig()
	nomadClient, err := nomad_api.NewClient(nomadClientConfig)
	if err != nil {
		log.Fatalf("Could not initialize nomad client: %s", err)
	}

	housekeeper.NomadClient = nomadClient

	err = envconfig.Process("housekeeper", &config)
	if err != nil {
		log.Fatalf("Could not initialize config: %s", err)
	}

	if config.Debug {
		log.SetLevel(log.DebugLevel)
	}
}

func main() {
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	if config.RunOnce {
		err := cleanup()
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	if !config.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	router.GET("/health", func(ctx *gin.Context) {
		log.Info("Received health check")
		_, err := housekeeper.NomadClient.Status().Leader()
		if err != nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"message": "Can't connect to Nomad API"})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"message": "ALL GOOD"})
	})

	go func() {
		log.Fatal(router.Run("0.0.0.0:8080"))
	}()

	for {
		select {
		case <-c:
			log.Info("Signal received, gracefully shutting down")
			return
		case <-time.After(config.Interval):
			err := cleanup()
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

func cleanup() (err error) {
	jobs := housekeeper.NomadClient.Jobs()
	all_jobs, _, err := jobs.List(&nomad_api.QueryOptions{
		Namespace:  nomad_api.AllNamespacesNamespace,
		AllowStale: true,
	})
	if err != nil {
		return fmt.Errorf("could not list jobs running on cluster: %s", err)
	}

	for _, current_job := range all_jobs {
		job, _, err := jobs.Info(current_job.Name, &nomad_api.QueryOptions{
			Namespace: current_job.Namespace,
		})
		if err != nil {
			log.Errorf("could not get details for job %s : %s", current_job.Name, err)
			continue
		}

		log.Debugf("Looking at %s", *job.Name)

		if shouldSkip(job) {
			log.Debugf("Skipping %s", *job.Name)
			continue
		}

		if !expired(job) {
			log.Debugf("Job %s not expired or set up for cleanup", current_job.Name)
			continue
		}

		if config.DryRun {
			log.Infof("Would have stopped %s", current_job.Name)
			continue
		}

		log.Debugf("Stopping %s", *job.Name)
		_, _, err = jobs.Deregister(current_job.ID, shouldPurge(job), &nomad_api.WriteOptions{
			Namespace: current_job.Namespace,
		})

		if err != nil {
			log.Warnf("could not remove job %s: %s", current_job.Name, err)
		}
	}
	return nil
}

func shouldSkip(job *nomad_api.Job) bool {
	if *job.Status != "running" {
		return true
	}

	if *job.Type == nomad_api.JobTypeBatch {
		return true
	}

	if job.IsPeriodic() {
		return true
	}

	return false
}

func shouldPurge(job *nomad_api.Job) bool {
	// Cron or batch jobs are ignored anyway
	if *job.ParentID != "" {
		return false
	}

	for key, value := range job.Meta {
		if key == HousekeeperPurge {
			return strings.ToLower(value) == "true"
		}
	}
	return false
}

func expired(job *nomad_api.Job) bool {
	now := time.Now()
	for key, value := range job.Meta {
		if key == HousekeeperTTL {
			ttl, err := str2duration.ParseDuration(value)
			if err != nil {
				log.Warnf("could not interpret ttl for job %s: %s", *job.ID, value)
				break
			}
			jobStart := time.Unix(*job.SubmitTime, 0)
			deadline := jobStart.Add(ttl)
			return now.After(deadline)
		}
		if key == HousekeeperExpires {
			expiration, err := dateparse.ParseAny(value)
			if err != nil {
				log.Warnf("could not interpret expiration date (%s) for job %s", value, *job.ID)
				break
			}
			return now.After(expiration)
		}
	}
	return false
}

// MAYBE: rules file that ignores tagged jobs but works on the others
