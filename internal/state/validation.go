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

func validatePublication(publication *Publication) error {
	if publication.Sequence <= 0 || publication.Timestamp.IsZero() || len(publication.Records) == 0 {
		return errors.New("invalid publication in history")
	}
	if err := validTopic(publication.Topic); err != nil {
		return err
	}
	if err := validName(publication.Agent); err != nil {
		return err
	}
	return validateRecords(publication.Records)
}

func (cursor *Cursor) validate() error {
	for slot, index := range cursor.Topics {
		if slot == "" || index < 0 {
			return errors.New("invalid cursor state")
		}
	}
	return nil
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
