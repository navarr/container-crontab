package events

import (
	"context"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rancher/container-crontab/cron"
)

// Handler handles messages
type Handler interface {
	Handle(Message)
}

// Message is a message from an event stream
type Message *events.Message

// DockerHandler handles docker messages
type DockerHandler struct {
	Crontab *cron.Crontab
}

type DockerHandlerOpts struct {
	RancherMode bool
	MetadataURL string
}

// NewDockerHandler returns a docker handler with crontab
func NewDockerHandler(opts *DockerHandlerOpts) (*DockerHandler, error) {
	crontab, err := cron.NewCrontab()
	if err != nil {
		return nil, err
	}

	if opts.RancherMode {
		logrus.Infof("Using Rancher Mode with metadata URL = %s", opts.MetadataURL)
		crontab, err = cron.NewRancherTypeCrontab(opts.MetadataURL)
		if err != nil {
			return nil, err
		}
	}

	dClient, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	defer dClient.Close()

	containers, err := dClient.ContainerList(context.Background(), types.ContainerListOptions{
		All: true,
	})
	if err != nil {
		logrus.Fatal(err)
		return nil, err
	}

	cronGroup, useGroups := os.LookupEnv("CRON_GROUP")

	// Scan containers
	logrus.Infof("Scanning for container cron entries")
	for _, container := range containers {
		_, ok := container.Labels["cron.schedule"]
		containerGroup, hasGroup := container.Labels["cron.group"]
		if ok && ((!useGroups && !hasGroup) || containerGroup == cronGroup) {
			crontab.AddJob(container.ID, container.Labels, "docker")
		}
	}

	return &DockerHandler{
		Crontab: crontab,
	}, nil
}

// Handle implements handler interface
func (dh DockerHandler) Handle(msg Message) {
	// Adding a cron.schedule label flags the container for deeper inspection
	// With this service
	if _, ok := msg.Actor.Attributes["cron.schedule"]; ok {
		if msg.Action == "start" || msg.Action == "create" {
			logrus.Debugf("Processing %s event for container: %s", msg.Action, msg.ID)
			dh.Crontab.AddJob(msg.ID, msg.Actor.Attributes, "docker")
		}

		if msg.Action == "stop" || msg.Action == "die" {
			logrus.Debugf("Proccessing %s event for container: %s", msg.Action, msg.ID)
			dh.Crontab.DeactivateJob(msg.ID, msg.Actor.Attributes)
		}

		if msg.Action == "destroy" {
			logrus.Debugf("Processing destroy event for container: %s", msg.ID)
			dh.Crontab.RemoveJob(msg.ID)
		}
	}
}

func (dh DockerHandler) GetJobStats(guage *prometheus.GaugeVec) (*prometheus.GaugeVec, error) {
	guage.With(prometheus.Labels{"state": "active"}).Set(dh.Crontab.GetNumberOfActiveJobs())
	guage.With(prometheus.Labels{"state": "inactive"}).Set(dh.Crontab.GetNumberOfInactiveJobs())
	return guage, nil
}
