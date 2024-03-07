package enats

import (
	"strings"
)

const (
	userPrefix = "raw."
)

// Given a nats subject, return the user-specified topic.
func ToTopic(subject string) string {
	return strings.TrimPrefix(subject, EventStreamPrefix)
}

// Given a user-specified topic, return the subject names to use for NATS consumers/publishers.
func ToSubject(topic string) string {
	// User specified topic
	if subject, ok := strings.CutPrefix(topic, userPrefix); ok {
		topic = subject
	} else if !strings.HasPrefix(topic, EventStreamPrefix) {
		topic = EventStreamPrefix + topic
	}

	// Wildcard
	if strings.HasSuffix(topic, ".") {
		topic += ">"
	}
	return topic
}

// Given a user-specified topic, return the queue name to use for NATS consumers.
func ToConsumerQueueName(pfx, topic string) string {
	queue := pfx + ToSubject(topic)
	queue = strings.ReplaceAll(queue, ".", "-")
	queue = strings.ReplaceAll(queue, "*", "any")
	queue = strings.ReplaceAll(queue, ">", "all")
	return queue
}
