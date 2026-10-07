package broker

import (
	"context"
	"fmt"
)

type ConsolePublisher struct{}

func NewConsolePublisher() *ConsolePublisher {
	return &ConsolePublisher{}
}

func (c *ConsolePublisher) Publish(ctx context.Context, aggregateID int, eventType, payload string) error {

	fmt.Printf("[KAFKA MOCK] Dispatched %s for Aggregate %d: %s\n", eventType, aggregateID, payload)
	return nil
}
