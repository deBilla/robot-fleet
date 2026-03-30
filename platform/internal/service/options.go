package service

import "go.temporal.io/sdk/client"

// RobotServiceOption configures optional dependencies for RobotService.
type RobotServiceOption func(*robotService)

// WithTemporalRobotClient sets the Temporal client for durable command dispatch.
func WithTemporalRobotClient(tc client.Client) RobotServiceOption {
	return func(s *robotService) { s.temporalClient = tc }
}

// WithCommandProducer sets the Kafka command producer for command dispatch.
func WithCommandProducer(p CommandProducer) RobotServiceOption {
	return func(s *robotService) { s.commandProducer = p }
}
