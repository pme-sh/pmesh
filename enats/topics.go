package enats

import (
	"strconv"
	"strings"

	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/xlog"
)

var AnyCastMachineID = config.MachineID(0)
var remoteQueueName = "ev." + AnyCastMachineID.String() + "."
var localQueueName = "ev." + config.GetMachineID().String() + "."

// Given a nats subject, return the user-specified topic.
func ToTopic(subject string) (topic string, dest config.MachineID) {
	dest = AnyCastMachineID

	// User specified topic
	if strings.HasPrefix(subject, "jet.") {
		topic = strings.TrimPrefix(subject, "jet.")
		return
	}

	// Remote topic
	if strings.HasPrefix(subject, remoteQueueName) {
		topic = subject[len(remoteQueueName):]
		return
	}

	// Local topic
	if strings.HasPrefix(subject, localQueueName) {
		topic = subject[len(localQueueName):]
		dest = config.GetMachineID()
		return
	}

	if strings.HasPrefix(subject, "ev.") {
		if len(subject) <= len(remoteQueueName) || subject[len(remoteQueueName)-1] != '.' {
			xlog.Warn().Str("subject", subject).Msg("Invalid subject")
			return
		}

		u64, err := strconv.ParseUint(subject[len("ev."):len(remoteQueueName)-1], 16, 32)
		if err != nil {
			xlog.Warn().Str("subject", subject).Err(err).Msg("Invalid subject")
			return
		}

		dest = config.MachineID(uint32(u64))
		topic = subject[len(remoteQueueName):]
		return
	}

	topic = subject
	return
}

// Given a user-specified topic, return the subject names to use for NATS consumers.
func ToConsumerSubjects(topic string) []string {
	if strings.HasSuffix(topic, ".") {
		topic += ">"
	}
	if strings.HasPrefix(topic, "jet.") {
		return []string{strings.TrimPrefix(topic, "jet.")}
	}
	if strings.HasPrefix(topic, "$local.") {
		return []string{localQueueName + strings.TrimPrefix(topic, "$local.")}
	}
	return []string{
		remoteQueueName + topic,
		localQueueName + topic,
	}
}

// Given a user-specified topic, return the subject name to use for NATS publishers.
func ToPublisherSubject(topic string) string {
	if strings.HasPrefix(topic, "jet.") {
		return strings.TrimPrefix(topic, "jet.")
	} else if !strings.HasPrefix(topic, "$local.") {
		return remoteQueueName + topic
	} else {
		return localQueueName + strings.TrimPrefix(topic, "$local.")
	}
}
func ToPublisherSubjectWithTarget(topic string, target config.MachineID) string {
	if strings.HasPrefix(topic, "jet.") {
		return strings.TrimPrefix(topic, "jet.")
	} else {
		return "ev." + config.GetMachineID().String() + "." + strings.TrimPrefix(topic, "$local.")
	}
}

func ToConsumerQueueName(pfx, topic string) string {
	queue := strings.ReplaceAll(pfx+topic, ".", "-")
	queue = strings.ReplaceAll(queue, "*", "all")
	queue = strings.ReplaceAll(queue, ">", "matchall")
	return queue
}
