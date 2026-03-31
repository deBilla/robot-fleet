package temporal

import (
	"fmt"

	"go.temporal.io/sdk/client"
)

// Task queue names — separate queues for independent scaling per concern.
const (
	TaskQueueCommand    = "fleetos-commands"
	TaskQueueDeployment = "fleetos-deployments"
	TaskQueueWebhook    = "fleetos-webhooks"
	TaskQueueBilling    = "fleetos-billing"
	TaskQueueTraining   = "fleetos-training"
)

// NewClient creates a Temporal client connected to the given server.
func NewClient(hostPort, namespace string) (client.Client, error) {
	c, err := client.Dial(client.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("create temporal client: %w", err)
	}
	return c, nil
}
