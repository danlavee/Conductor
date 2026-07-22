package state

import (
	"errors"
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
	if publication.Sequence <= 0 || publication.Timestamp.IsZero() || len(publication.Records) == 0 {
		return errors.New("invalid commit history state")
	}
	if err := validTopic(publication.Topic); err != nil {
		return err
	}
	if err := validName(publication.Agent); err != nil {
		return err
	}
	return validateRecords(publication.Records)
}

func (transaction *Transaction) validate() error {
	if transaction.PID <= 0 || transaction.Started.IsZero() || transaction.Sequence < 0 || transaction.Records == nil || transaction.Created == nil {
		return errors.New("invalid transaction state")
	}
	if err := validTopic(transaction.Topic); err != nil {
		return err
	}
	if err := validName(transaction.Agent); err != nil {
		return err
	}
	for index, record := range transaction.Records {
		if index != record.Index {
			return errors.New("transaction record key does not match its index")
		}
	}
	for index, created := range transaction.Created {
		if !created {
			return errors.New("transaction created marker must be true")
		}
		if _, ok := transaction.Records[index]; !ok {
			return errors.New("transaction created marker has no record")
		}
	}
	return validateRecords(sortedRecords(transaction.Records))
}

func (cursor *Cursor) validate() error {
	if cursor.Summary < 0 || cursor.InboxOffset < 0 {
		return errors.New("invalid cursor state")
	}
	for slot, index := range cursor.Topics {
		if slot == "" || index < 0 {
			return errors.New("invalid cursor state")
		}
	}
	previous := int64(0)
	for _, interval := range cursor.SummaryRanges {
		if interval.From <= previous || interval.To < interval.From {
			return errors.New("invalid cursor ranges")
		}
		previous = interval.To
	}
	return nil
}

func (event *Event) validate() error {
	if err := validateSummary(&event.Summary); err != nil {
		return err
	}
	for _, recipient := range event.Recipients {
		if err := validName(recipient); err != nil {
			return err
		}
	}
	return nil
}

func validateSummary(summary *Summary) error {
	if summary.Sequence <= 0 || (summary.Type != "join" && summary.Type != "update" && summary.Type != "leave") {
		return errors.New("invalid summary state")
	}
	if err := validName(summary.Agent); err != nil {
		return err
	}
	if summary.Topic == "registry" {
		return nil
	}
	return validTopic(summary.Topic)
}

func validateRecords(records []Record) error {
	seen := make(map[int64]bool, len(records))
	for _, record := range records {
		if record.Index <= 0 || seen[record.Index] {
			return errors.New("invalid record index")
		}
		seen[record.Index] = true
	}
	return nil
}
