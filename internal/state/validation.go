package state

import (
	"errors"
	"fmt"
	"strings"
)

type stateValidator interface {
	validate() error
}

func validateAgent(agent *Agent) error {
	if err := validName(agent.Name); err != nil {
		return err
	}
	if strings.TrimSpace(agent.Responsibility) == "" || agent.Timestamp.IsZero() {
		return errors.New("invalid agent state")
	}
	return nil
}

func (lock *Lock) validate() error {
	if lock.PID <= 0 || lock.ProcessStart == "" || lock.LeaseID == 0 || lock.TimeoutSec <= 0 || lock.Timestamp.IsZero() {
		return errors.New("invalid lock state")
	}
	return validName(lock.Agent)
}

func validatePublication(publication *Publication) error {
	if publication.Index <= 0 || publication.Timestamp.IsZero() || len(publication.Messages) == 0 {
		return errors.New("invalid publication state")
	}
	if err := validResource(publication.Resource); err != nil {
		return err
	}
	if err := validName(publication.Agent); err != nil {
		return err
	}
	return validateMutations(publication.Messages)
}

func (transaction *Transaction) validate() error {
	if transaction.PID <= 0 || transaction.Started.IsZero() || transaction.Index < 0 || transaction.Messages == nil {
		return errors.New("invalid transaction state")
	}
	if err := validResource(transaction.Resource); err != nil {
		return err
	}
	if err := validName(transaction.Agent); err != nil {
		return err
	}
	return validateMutations(transaction.Messages)
}

func (cursor *Cursor) validate() error {
	if cursor.Signal < 0 || cursor.InboxOffset < 0 {
		return errors.New("invalid cursor state")
	}
	for slot, index := range cursor.Resources {
		if slot == "" || index < 0 {
			return errors.New("invalid cursor state")
		}
	}
	previous := int64(0)
	for _, interval := range cursor.SignalRanges {
		if interval.From <= previous || interval.To < interval.From {
			return errors.New("invalid cursor ranges")
		}
		previous = interval.To
	}
	return nil
}

func (event *Event) validate() error {
	if err := validateSignal(&event.Signal); err != nil {
		return err
	}
	for _, recipient := range event.Recipients {
		if err := validName(recipient); err != nil {
			return err
		}
	}
	return nil
}

func validateSignal(signal *Signal) error {
	if signal.Index <= 0 || (signal.Type != "join" && signal.Type != "update" && signal.Type != "leave") {
		return errors.New("invalid signal state")
	}
	if err := validName(signal.Agent); err != nil {
		return err
	}
	if signal.Resource == "registry" {
		return validName(signal.Key)
	}
	if err := validResource(signal.Resource); err != nil {
		return err
	}
	if signal.Key == "*" {
		return nil
	}
	return validName(signal.Key)
}

func validateMutations(messages map[string]MessageMutation) error {
	for key, mutation := range messages {
		if err := validName(key); err != nil {
			return err
		}
		switch mutation.Operation {
		case MutationSet:
			if mutation.Payload == nil {
				return fmt.Errorf("set message %s requires payload", key)
			}
		case MutationScratch:
			if mutation.Kind != "" || mutation.Payload != nil {
				return fmt.Errorf("scratch message %s cannot carry kind or payload", key)
			}
		default:
			return fmt.Errorf("invalid message operation for %s", key)
		}
	}
	return nil
}

func (message *materializedMessage) validate() error {
	if err := validName(message.Key); err != nil {
		return err
	}
	if err := validName(message.Agent); err != nil {
		return err
	}
	if message.Index <= 0 || message.Timestamp.IsZero() {
		return errors.New("invalid materialized message")
	}
	if message.Scratched && (message.Kind != "" || message.Payload.Text != "") {
		return errors.New("scratched message carries content")
	}
	return nil
}
