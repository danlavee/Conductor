package state

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (c *Client) Subscription() (Subscription, error) {
	agent, err := c.requireAgent()
	if err != nil {
		return Subscription{}, err
	}
	return c.loadSubscription(agent)
}

func (c *Client) SubscribeTopicGroup(group string) (Subscription, error) {
	if strings.TrimSpace(group) != group || group == "" || strings.ContainsAny(group, `/\\`) {
		return Subscription{}, errors.New("invalid topic group")
	}
	return c.updateSubscription(func(subscription *Subscription) {
		subscription.TopicGroups = appendUnique(subscription.TopicGroups, group)
	})
}

func (c *Client) SubscribeTopic(topic string) (Subscription, error) {
	if err := validTopic(topic); err != nil {
		return Subscription{}, err
	}
	return c.updateSubscription(func(subscription *Subscription) {
		subscription.Topics = appendUnique(subscription.Topics, topic)
	})
}

func (c *Client) ListTopicGroups() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(c.Home, "topics"))
	if err != nil {
		return nil, err
	}
	groups := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			groups = append(groups, entry.Name())
		}
	}
	sort.Strings(groups)
	return groups, nil
}

func (c *Client) ListTopics(group string) ([]string, error) {
	if strings.TrimSpace(group) != group || group == "" || strings.ContainsAny(group, `/\\`) {
		return nil, errors.New("invalid topic group")
	}
	entries, err := os.ReadDir(filepath.Join(c.Home, "topics", group))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	topics := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			topics = append(topics, group+"/"+entry.Name())
		}
	}
	sort.Strings(topics)
	return topics, nil
}

func (c *Client) updateSubscription(update func(*Subscription)) (Subscription, error) {
	agent, err := c.requireAgent()
	if err != nil {
		return Subscription{}, err
	}
	release, err := c.acquireLeaseGuard(filepath.Join(c.Home, "subscriptions", agent+".guard"))
	if err != nil {
		return Subscription{}, err
	}
	defer release()
	subscription, err := c.loadSubscription(agent)
	if err != nil {
		return Subscription{}, err
	}
	update(&subscription)
	sort.Strings(subscription.TopicGroups)
	sort.Strings(subscription.Topics)
	if err := writeJSONAtomic(c.subscriptionPath(agent), subscription); err != nil {
		return Subscription{}, err
	}
	return subscription, nil
}

func (c *Client) loadSubscription(agent string) (Subscription, error) {
	subscription := Subscription{TopicGroups: []string{}, Topics: []string{}}
	err := readJSON(c.subscriptionPath(agent), &subscription)
	if errors.Is(err, os.ErrNotExist) {
		return subscription, nil
	}
	return subscription, err
}

func (c *Client) isSubscribed(agent, topic string) (bool, error) {
	subscription, err := c.loadSubscription(agent)
	if err != nil {
		return false, err
	}
	group := strings.SplitN(topic, "/", 2)[0]
	return contains(subscription.Topics, topic) || contains(subscription.TopicGroups, group), nil
}

func (c *Client) subscribedRecipientNames(topic string) ([]string, error) {
	agents, err := c.ListAgents()
	if err != nil {
		return nil, err
	}
	recipients := make([]string, 0, len(agents))
	for _, agent := range agents {
		subscribed, err := c.isSubscribed(agent.Name, topic)
		if err != nil {
			return nil, err
		}
		if subscribed {
			recipients = append(recipients, agent.Name)
		}
	}
	return recipients, nil
}

func (c *Client) subscriptionPath(agent string) string {
	return filepath.Join(c.Home, "subscriptions", agent+".json")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
