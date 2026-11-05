package state

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// errPrivateSubscription refuses every subscription into the private group:
// an agent's own private topic already reaches it, and another's is not its
// to read.
var errPrivateSubscription = &ProtocolError{Code: "INVALID", Text: "private/<name> reaches only <name>, without subscribing"}

func (c *Client) SubscribeTopicGroup(group string) (Subscription, error) {
	if strings.TrimSpace(group) != group || group == "" || strings.ContainsAny(group, `/\\`) {
		return Subscription{}, errors.New("invalid topic group")
	}
	if group == privateGroup {
		return Subscription{}, errPrivateSubscription
	}
	return c.updateSubscription(func(subscription *Subscription) {
		subscription.TopicGroups = appendUnique(subscription.TopicGroups, group)
	})
}

func (c *Client) SubscribeTopic(topic string) (Subscription, error) {
	if err := validTopic(topic); err != nil {
		return Subscription{}, err
	}
	if strings.HasPrefix(topic, privateGroup+"/") {
		return Subscription{}, errPrivateSubscription
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
	release, err := c.acquireStateMutex(filepath.Join(c.Home, "subscriptions", agent+".guard"))
	if err != nil {
		return Subscription{}, err
	}
	defer release()
	subscription, err := c.loadSubscription(agent)
	if err != nil {
		return Subscription{}, err
	}
	before, err := c.topicsReadableBy(agent, subscription)
	if err != nil {
		return Subscription{}, err
	}
	update(&subscription)
	after, err := c.topicsReadableBy(agent, subscription)
	if err != nil {
		return Subscription{}, err
	}
	// A subscription is not retroactive: what a topic already holds is read
	// with get, never delivered, so its cursor starts at the current head.
	heads := map[string]int64{}
	for _, topic := range after {
		if slices.Contains(before, topic) {
			continue
		}
		if heads[topic], err = c.topicHead(topic); err != nil {
			return Subscription{}, err
		}
	}
	if err := c.acknowledgeHeads(agent, heads); err != nil {
		return Subscription{}, err
	}
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

// isSubscribed answers whether a topic's content is the agent's to read, and
// so whether a publication there wakes it. The automatic topics are,
// unconditionally.
func (c *Client) isSubscribed(agent, topic string) (bool, error) {
	if slices.Contains(automaticTopics(agent), topic) {
		return true, nil
	}
	subscription, err := c.loadSubscription(agent)
	if err != nil {
		return false, err
	}
	group := strings.SplitN(topic, "/", 2)[0]
	return slices.Contains(subscription.Topics, topic) || slices.Contains(subscription.TopicGroups, group), nil
}

// readersOf names every registered agent a publication on topic wakes. The
// publisher is never one: an agent is not an addressee of its own act, and an
// agent alone on a topic it created must not read its own name back as an
// audience.
func (c *Client) readersOf(topic, publisher string) ([]string, error) {
	agents, err := c.ListAgents()
	if err != nil {
		return nil, err
	}
	readers := []string{}
	for _, agent := range agents {
		if agent.Name == publisher {
			continue
		}
		subscribed, err := c.isSubscribed(agent.Name, topic)
		if err != nil {
			return nil, err
		}
		if subscribed {
			readers = append(readers, agent.Name)
		}
	}
	return readers, nil
}

// readableTopics lists, sorted, every topic whose delta is the agent's to
// read: its automatic topics, its subscribed topics, and every topic that
// currently exists in its subscribed groups.
func (c *Client) readableTopics(agent string) ([]string, error) {
	subscription, err := c.loadSubscription(agent)
	if err != nil {
		return nil, err
	}
	return c.topicsReadableBy(agent, subscription)
}

func (c *Client) topicsReadableBy(agent string, subscription Subscription) ([]string, error) {
	topics := append(automaticTopics(agent), subscription.Topics...)
	for _, group := range subscription.TopicGroups {
		listed, err := c.ListTopics(group)
		if err != nil {
			return nil, err
		}
		topics = append(topics, listed...)
	}
	slices.Sort(topics)
	return slices.Compact(topics), nil
}

// topicHead answers the sequence of the topic's last publication, zero when it
// has none.
func (c *Client) topicHead(topic string) (int64, error) {
	release, err := c.lockResource(topic)
	if err != nil {
		return 0, err
	}
	history, err := c.readHistory(topic)
	if err := errors.Join(err, release()); err != nil || len(history) == 0 {
		return 0, err
	}
	return history[len(history)-1].Sequence, nil
}

func (c *Client) subscriptionPath(agent string) string {
	return filepath.Join(c.Home, "subscriptions", agent+".json")
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
