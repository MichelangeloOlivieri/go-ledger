package broker

import (
	"context"
	"log/slog"
)

// for now the publisher simply logs the message to standard output, until a future message broker (e.g. notification service) is implemented
type ConsolePublisher struct{}

func NewConsolePublisher() *ConsolePublisher {
	return &ConsolePublisher{}
}

func (c *ConsolePublisher) Publish(ctx context.Context, aggregateID string, eventType string, payload string) error {
	slog.Info("Domain event published in local", "type", eventType, "aggregate", aggregateID, "payload", payload)
	return nil
}
