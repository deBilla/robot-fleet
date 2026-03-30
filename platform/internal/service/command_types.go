package service

// CommandMessage is the Kafka message format for the robot.commands topic.
// Both the REST API (producer) and the processor (consumer) use this type.
type CommandMessage struct {
	RobotID          string            `json:"robot_id"`
	CommandID        int64             `json:"command_id"`
	CmdType          string            `json:"cmd_type"`
	Params           map[string]any    `json:"params"`
	TenantID         string            `json:"tenant_id"`
	DedupKey         string            `json:"dedup_key"`
	WithInference    bool              `json:"with_inference,omitempty"`
	InferenceRequest *InferenceRequest `json:"inference_request,omitempty"`
}

// CommandProducer publishes command messages to a message broker (e.g. Kafka).
type CommandProducer interface {
	Publish(robotID string, data []byte) error
}
