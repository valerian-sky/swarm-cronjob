package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/crazy-max/cron"
	. "github.com/crazy-max/swarm-cronjob/internal"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/mitchellh/mapstructure"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	Logger.Info().Msgf("Starting %s v%s", AppName, AppVersion)

	dcli, err := DockerEnvClient()
	if err != nil {
		Logger.Fatal().Err(err).Msg("Cannot create Docker client")
	}

	services, err := ScheduledServices(dcli)
	if err != nil {
		Logger.Error().Err(err).Msg("Cannot retrieve scheduled services")
	}

	// Set timezone
	loc, err := time.LoadLocation(GetEnv("TZ", "UTC"))
	if err != nil {
		Logger.Fatal().Err(err).Msgf("Failed to load time zone %s", GetEnv("TZ", "UTC"))
	}

	// Start cron
	c := cron.NewWithLocation(loc)
	for _, service := range services {
		if _, err := CrudJob(service.Spec.Name, dcli, c); err != nil {
			Logger.Error().Err(err).Msgf("Cannot manage job for service %s", service.Spec.Name)
		}
	}
	c.Start()

	// Handle os signals
	channel := make(chan os.Signal)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-channel
		c.Stop()
		os.Exit(1)
	}()

	// Listen Docker events
	filter := filters.NewArgs()
	filter.Add("type", "service")

	msgs, errs := dcli.Events(context.Background(), types.EventsOptions{
		Filters: filter,
	})

	var event ServiceEvent
	for {
		select {
		case err := <-errs:
			Logger.Fatal().Err(err).Msg("Event channel failed")
		case msg := <-msgs:
			err := mapstructure.Decode(msg.Actor.Attributes, &event)
			if err != nil {
				Logger.Warn().Msgf("Cannot decode event, %v", err)
				continue
			}
			Logger.Debug().Msgf("Event triggered for %s (newstate='%s' oldstate='%s')", event.Service, event.UpdateState.New, event.UpdateState.Old)
			processed, err := CrudJob(event.Service, dcli, c)
			if err != nil {
				Logger.Error().Err(err).Msgf("Cannot manage job for service %s", event.Service)
				continue
			} else if processed {
				Logger.Debug().Msgf("Number of cronjob tasks : %d", len(c.Entries()))
			}
		}
	}
}
