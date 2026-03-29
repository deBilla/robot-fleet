package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

// WebhookFanoutInput is the input for WebhookFanoutWorkflow.
type WebhookFanoutInput struct {
	EventType string             `json:"event_type"`
	Body      []byte             `json:"body"`
	Timestamp int64              `json:"timestamp"`
	Hooks     []WebhookTarget    `json:"hooks"`
}

// WebhookTarget identifies a single webhook to deliver to.
type WebhookTarget struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

// WebhookFanoutWorkflow spawns a child workflow per webhook for parallel delivery.
func WebhookFanoutWorkflow(ctx workflow.Context, input WebhookFanoutInput) error {
	for _, hook := range input.Hooks {
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("wh-%s-%s-%d", hook.ID, input.EventType, input.Timestamp),
		})
		// Fire-and-forget: parent does not wait for children
		workflow.ExecuteChildWorkflow(childCtx, WebhookDeliverWorkflow, activities.DeliverWebhookInput{
			WebhookID: hook.ID,
			URL:       hook.URL,
			Secret:    hook.Secret,
			EventType: input.EventType,
			Body:      input.Body,
		})
	}
	return nil
}

// WebhookDeliverWorkflow delivers to a single webhook endpoint with retries.
func WebhookDeliverWorkflow(ctx workflow.Context, input activities.DeliverWebhookInput) error {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        2 * time.Second,
			BackoffCoefficient:     2.0,
			MaximumAttempts:        5,
			NonRetryableErrorTypes: []string{"PermanentWebhookError"},
		},
	})
	return workflow.ExecuteActivity(actCtx, "DeliverWebhook", input).Get(ctx, nil)
}
